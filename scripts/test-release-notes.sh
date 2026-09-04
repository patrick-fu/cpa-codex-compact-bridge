#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tag=v0.1.4
notes=$(mktemp "${TMPDIR:-/tmp}/release-notes-test.XXXXXX")
tampered=$(mktemp "${TMPDIR:-/tmp}/release-notes-test.XXXXXX")
fixture=$(mktemp -d "${TMPDIR:-/tmp}/release-notes-fixture.XXXXXX")
trap 'rm -f "$notes" "$tampered"; rm -rf "$fixture"' EXIT HUP INT TERM

cd "$root"
sh scripts/release-notes.sh --tag "$tag" --output "$notes"
sh scripts/release-notes.sh --verify --tag "$tag" --notes "$notes"
sh scripts/release-notes.sh --tag "$tag" --commit "$(git rev-parse "$tag^{commit}")" --verify --notes "$notes"

previous=v0.1.3
expected=$(git rev-list --count "$previous..$tag")
actual=$(grep -Ec '^-[[:space:]]`[0-9a-f]{40}` ' "$notes" || true)
[ "$actual" -eq "$expected" ]
grep -F -- "- Commit range: \`$previous..$tag\`" "$notes" >/dev/null

cp "$notes" "$tampered"
printf '\nmissing commit\n' >>"$tampered"
if sh scripts/release-notes.sh --verify --tag "$tag" --notes "$tampered" >/dev/null 2>&1; then
  echo "tampered release notes unexpectedly passed verification" >&2
  exit 1
fi

if sh scripts/release-notes.sh --tag v01.0.0 --output "$notes" >/dev/null 2>&1; then
  echo "leading-zero semver tag unexpectedly passed validation" >&2
  exit 1
fi

git init -q "$fixture"
git -C "$fixture" config user.name release-notes-test
git -C "$fixture" config user.email release-notes-test@example.invalid
git -C "$fixture" commit --allow-empty -qm 'First release'
git -C "$fixture" tag v0.8.0
git -C "$fixture" commit --allow-empty -qm 'Accidental future release'
git -C "$fixture" tag v1.0.0
git -C "$fixture" commit --allow-empty -qm 'Attempted version rollback'
git -C "$fixture" tag v0.9.0
if (cd "$fixture" && sh "$root/scripts/release-notes.sh" --tag v0.9.0 --output "$notes") >/dev/null 2>&1; then
  echo "non-monotonic semver history unexpectedly passed validation" >&2
  exit 1
fi

echo "PASS release notes: $previous..$tag ($expected commits)"
