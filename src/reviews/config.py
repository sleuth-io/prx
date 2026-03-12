import os
import tomllib
from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class AnthropicConfig:
    api_key: str
    model: str = "claude-sonnet-4-20250514"


@dataclass
class ReviewConfig:
    max_diff_chars: int = 30000


@dataclass
class Weights:
    blast_radius: float = 1.0
    test_coverage: float = 1.0
    sensitivity: float = 1.0
    complexity: float = 1.0
    scope_focus: float = 1.0


@dataclass
class Thresholds:
    approve_below: float = 2.0
    review_above: float = 3.5


@dataclass
class Config:
    anthropic: AnthropicConfig
    review: ReviewConfig = field(default_factory=ReviewConfig)
    weights: Weights = field(default_factory=Weights)
    thresholds: Thresholds = field(default_factory=Thresholds)


def _default_config_path() -> Path:
    xdg = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
    return xdg / "reviews" / "config.toml"


def load_config(config_path: Path | None = None) -> Config:
    if config_path is None:
        config_path = _default_config_path()

    if not config_path.exists():
        raise FileNotFoundError(
            f"Config file not found: {config_path}\n"
            "Run setup.sh to create it, or copy config.toml.example"
        )

    with open(config_path, "rb") as f:
        data = tomllib.load(f)

    anthropic_data = data.get("anthropic", {})
    if not anthropic_data.get("api_key"):
        raise ValueError("anthropic.api_key is required in config.toml")

    return Config(
        anthropic=AnthropicConfig(**anthropic_data),
        review=ReviewConfig(**data.get("review", {})),
        weights=Weights(**data.get("weights", {})),
        thresholds=Thresholds(**data.get("thresholds", {})),
    )
