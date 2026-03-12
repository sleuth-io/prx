#!/bin/bash
# setup.sh — run once to create ~/.config/reviews/config.toml
KEY=$(grep ANTHROPIC_API_KEY /home/mrdon/dev/pulse/.env | cut -d= -f2 | tr -d '"' | tr -d "'")
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/reviews"
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/config.toml" <<EOF
[anthropic]
api_key = "$KEY"
model = "claude-sonnet-4-20250514"

[review]
max_diff_chars = 30000

[weights]
blast_radius = 1.0
test_coverage = 1.0
sensitivity = 1.0
complexity = 1.0
scope_focus = 1.0

[thresholds]
approve_below = 2.0
review_above = 3.5
EOF
echo "config.toml written to $CONFIG_DIR/config.toml"
