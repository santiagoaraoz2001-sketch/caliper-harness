import types
import unittest
from unittest.mock import patch
import torch
import torch.nn.functional as F
from mps_attention import chunked_causal_attention, install


class ChunkedAttentionTests(unittest.TestCase):
    def test_full_context_causal_gqa_equivalence(self):
        torch.manual_seed(4)
        for length in (1, 7, 17, 31):
            q = torch.randn(2, 4, length, 8)
            k = torch.randn(2, 2, length, 8)
            v = torch.randn(2, 2, length, 8)
            expected = F.scaled_dot_product_attention(q, k, v, is_causal=True, scale=.25, enable_gqa=True)
            actual = chunked_causal_attention(q, k, v, .25, 7)
            torch.testing.assert_close(actual, expected, rtol=1e-5, atol=1e-6)
            # A final-token change cannot affect preceding query output.
            v[..., -1, :] += 1000
            changed = chunked_causal_attention(q, k, v, .25, 7)
            torch.testing.assert_close(changed[..., :-1, :], actual[..., :-1, :], rtol=0, atol=0)

    def test_full_length_never_dispatches_square_attention(self):
        # Meta tensors: no full-context GPU or model allocation.
        q = torch.empty(1,32,15755,64,device='meta')
        k = torch.empty(1,8,15755,64,device='meta')
        seen = []
        def attention(query, key, value, **kwargs):
            seen.append((query.shape[-2], key.shape[-2], tuple(kwargs['attn_mask'].shape)))
            return torch.empty_like(query)
        with patch('torch.nn.functional.scaled_dot_product_attention', side_effect=attention):
            output = chunked_causal_attention(q,k,k,.125,1024)
        self.assertEqual(output.shape,q.shape)
        self.assertEqual(len(seen),16)
        self.assertEqual(sum(row[0] for row in seen),15755)
        self.assertTrue(all(n<=1024 and keys==15755 and mask==(n,15755) for n,keys,mask in seen))

    def test_install_is_scoped_idempotent_and_preserves_cpu(self):
        calls = []
        def original(*args):
            calls.append(args)
            return 'native'
        hybrid = types.SimpleNamespace(causal_attention=original, CHUNK=7)
        model = type('Native', (), {'__module__':'native.model'})()
        with patch('mps_attention.importlib.import_module', return_value=hybrid):
            self.assertTrue(install(model))
            hooked = hybrid.causal_attention
            self.assertTrue(install(model))
            self.assertIs(hybrid.causal_attention, hooked)
        q = torch.ones(1,1,20,2)
        self.assertEqual(hooked(q,q,q,None,1), 'native')
        self.assertEqual(len(calls),1)

if __name__ == '__main__':
    unittest.main()
