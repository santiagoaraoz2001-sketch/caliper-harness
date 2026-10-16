"""Keep native full-context causal attention on MPS without a full square score tensor."""
import importlib


def install(model):
    # Native System One hybrid checkpoints own this function. Other checkpoint
    # architectures retain their implementation; never patch torch globally.
    package = model.__class__.__module__.rsplit('.', 1)[0]
    try:
        hybrid = importlib.import_module(package + '.hybrid')
    except ModuleNotFoundError as error:
        if error.name == package + '.hybrid':
            return False
        raise
    original = getattr(hybrid, 'causal_attention', None)
    chunk = getattr(hybrid, 'CHUNK', None)
    if not callable(original) or type(chunk) is not int or chunk <= 0:
        return False
    if getattr(original, '_mps_chunked', False):
        return True

    def causal_attention(q, k, v, window, scale):
        if q.device.type != 'mps' or window is not None or q.shape[-2] <= chunk:
            return original(q, k, v, window, scale)
        return chunked_causal_attention(q, k, v, scale, chunk)

    causal_attention._mps_chunked = True
    hybrid.causal_attention = causal_attention
    return True


def chunked_causal_attention(q, k, v, scale, chunk):
    import torch
    import torch.nn.functional as F
    # Every query still sees every causally preceding key. Only temporary score
    # storage changes; the checkpoint's own CHUNK controls execution tiling.
    keys = torch.arange(k.shape[-2], device=q.device)
    output = torch.empty_like(q)
    for start in range(0, q.shape[-2], chunk):
        stop = min(start + chunk, q.shape[-2])
        allowed = keys[None, :] <= torch.arange(start, stop, device=q.device)[:, None]
        output[..., start:stop, :] = F.scaled_dot_product_attention(
            q[..., start:stop, :], k, v, attn_mask=allowed,
            scale=scale, enable_gqa=True)
        if q.device.type == "mps":
            # Settle each tile before enqueuing another, so asynchronous command
            # buffers cannot retain every tile's temporary scores simultaneously.
            torch.mps.synchronize()
    return output
