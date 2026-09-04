#!/bin/sh
# Generate deterministic, auditable GitHub Release notes from a semver tag range.
set -eu

usage() {
  echo "Usage: $0 --tag vX.Y.Z [--commit SHA] --output FILE" >&2
  echo "       $0 --verify --tag vX.Y.Z [--commit SHA] --notes FILE" >&2
  exit 2
}

tag=
commit=
output=
notes=
verify=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --tag) tag=${2-}; shift 2 ;;
    --commit) commit=${2-}; shift 2 ;;
    --output) output=${2-}; shift 2 ;;
    --notes) notes=${2-}; shift 2 ;;
    --verify) verify=1; shift ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

[ -n "$tag" ] || usage
case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "release tag must be a strict vX.Y.Z version: $tag" >&2; exit 1 ;;
esac
if ! printf '%s' "${tag#v}" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "release tag must be a strict vX.Y.Z version: $tag" >&2
  exit 1
fi
if [ -z "$commit" ]; then
  commit=$tag
fi
commit=$(git rev-parse --verify -q "$commit^{commit}") || {
  echo "release commit does not resolve to a commit: $commit" >&2
  exit 1
}

previous_tag() {
  git tag --merged "$commit" | awk -v current="$tag" '
    function split_version(value, parts) {
      sub(/^v/, "", value)
      split(value, parts, ".")
    }
    function compare(left, right, left_parts, right_parts) {
      split_version(left, left_parts)
      split_version(right, right_parts)
      if (left_parts[1] != right_parts[1]) return (left_parts[1] + 0 > right_parts[1] + 0) ? 1 : -1
      if (left_parts[2] != right_parts[2]) return (left_parts[2] + 0 > right_parts[2] + 0) ? 1 : -1
      if (left_parts[3] != right_parts[3]) return (left_parts[3] + 0 > right_parts[3] + 0) ? 1 : -1
      return 0
    }
    $0 ~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ {
      relation = compare($0, current)
      if ($0 != current && relation >= 0) {
        printf "reachable semver tag %s is not older than release tag %s; refusing a non-monotonic release history\\n", $0, current > "/dev/stderr"
        invalid = 1
      }
      if (relation < 0 && (best == "" || compare($0, best) > 0)) best = $0
    }
    END {
      if (invalid) exit 1
      print best
    }
  '
}

previous=$(previous_tag)
if [ -z "$previous" ]; then
  echo "no earlier strict semver tag reachable from $tag; cannot create an auditable release range" >&2
  exit 1
fi

commit_count=$(git rev-list --count "$previous..$commit")
if [ "$commit_count" -eq 0 ]; then
  echo "release range $previous..$tag ($commit) has no commits" >&2
  exit 1
fi

generate() {
  destination=$1
  {
    printf '# cpa-codex-compact-bridge %s\n\n' "$tag"
    printf '## Changes since %s\n\n' "$previous"
    printf 'This release contains %s commits from `%s..%s`.\n\n' "$commit_count" "$previous" "$tag"
    printf '## Included commits\n\n'
    git log --format='%H %s' "$previous..$commit" |
      while read -r commit subject; do
        printf '%s\n' "- \`$commit\` $subject"
      done
    printf '\n## Audit range\n\n'
    printf -- '- Previous release tag: `%s`\n' "$previous"
    printf -- '- Current release tag: `%s`\n' "$tag"
    printf -- '- Commit range: `%s..%s`\n' "$previous" "$tag"
    printf -- '- Exact commit count: %s\n' "$commit_count"
  } >"$destination"
}

if [ "$verify" -eq 1 ]; then
  [ -n "$notes" ] || usage
  [ -f "$notes" ] || { echo "release notes file does not exist: $notes" >&2; exit 1; }
  temporary=$(mktemp "${TMPDIR:-/tmp}/release-notes.XXXXXX")
  trap 'rm -f "$temporary"' EXIT HUP INT TERM
  generate "$temporary"
  if ! cmp -s "$temporary" "$notes"; then
    echo "release notes do not exactly cover the computed range $previous..$tag" >&2
    exit 1
  fi
  exit 0
fi

[ -n "$output" ] || usage
generate "$output"
