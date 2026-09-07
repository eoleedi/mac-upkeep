#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

fresh="$tmp/fresh"
mkdir -p "$fresh"
make -C "$root" install-files HOME="$fresh"
test -x "$fresh/.local/bin/mac-upkeep"
test -f "$fresh/.config/mac-upkeep/config.toml"

migrated="$tmp/migrated"
mkdir -p "$migrated/.config/ua"
printf '%s\n' 'old-config-sentinel' > "$migrated/.config/ua/config.toml"
make -C "$root" install-files HOME="$migrated"
cmp "$migrated/.config/ua/config.toml" "$migrated/.config/mac-upkeep/config.toml"

preserved="$tmp/preserved"
mkdir -p "$preserved/.config/mac-upkeep"
printf '%s\n' 'new-config-sentinel' > "$preserved/.config/mac-upkeep/config.toml"
cp "$preserved/.config/mac-upkeep/config.toml" "$tmp/preserved.expected"
make -C "$root" install-files HOME="$preserved"
cmp "$tmp/preserved.expected" "$preserved/.config/mac-upkeep/config.toml"
