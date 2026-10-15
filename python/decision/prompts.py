"""Read the same editable prompt asset as Go, once per sidecar manager.

Only the three static decision strings are consumed here. Go owns rendering of
all dynamic templates; this module supplies no independent instruction defaults.
"""
from pathlib import Path
from types import MappingProxyType
from dataclasses import dataclass

import yaml


@dataclass(frozen=True)
class Catalog:
    source: str
    texts: object
    def text(self, key):
        return self.texts[key]


class _UniqueLoader(yaml.SafeLoader):
    pass


def _mapping(loader, node, deep=False):
    result = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if not isinstance(key, str) or key in result:
            raise ValueError(f"prompt catalog has an invalid or duplicate key: {key!r}")
        result[key] = loader.construct_object(value_node, deep=deep)
    return result


_UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, _mapping)


def load(path=None):
    source = Path(path) if path is not None else Path(__file__).resolve().parents[2] / "prompts.yaml"
    source = source.expanduser().resolve()
    try:
        data = yaml.load(source.read_text(encoding="utf-8"), Loader=_UniqueLoader)
        if not isinstance(data, dict) or set(data) != {"texts"} or not isinstance(data["texts"], dict):
            raise ValueError("expected only the texts mapping")
        texts = data["texts"]
        if not texts or any(not isinstance(value, str) or not value.strip() for value in texts.values()):
            raise ValueError("every prompt must be a nonempty string")
        for key in ("decision.default.instructions", "decision.readiness_probe.context", "decision.readiness_probe.instructions"):
            if key not in texts:
                raise ValueError(f"missing key {key}")
            if "{{" in texts[key] or "}}" in texts[key]:
                raise ValueError(f"static Python prompt {key} cannot contain template actions")
        return Catalog(str(source), MappingProxyType(dict(texts)))
    except (OSError, UnicodeError, ValueError, yaml.YAMLError) as error:
        raise ValueError(f"prompt catalog {source}: {error}") from error
