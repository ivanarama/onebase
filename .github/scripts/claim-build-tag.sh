#!/usr/bin/env bash
# softprops ignores target_commitish when the tag already exists. Claim the
# build tag before publishing assets, even if main advanced during the build.
set -euo pipefail
tag_ref="refs/tags/build-${GITHUB_RUN_NUMBER:?}"
if git push origin "${GITHUB_SHA:?}:$tag_ref"; then
  exit 0
fi

# A competing push (or an existing annotated tag) is acceptable only when
# the remote tag peels to exactly this run's commit. Never force-update it.
git fetch --no-tags --depth=1 origin "$tag_ref"
tag_commit=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
if [[ "$tag_commit" != "$GITHUB_SHA" ]]; then
  echo "::error::$tag_ref points to $tag_commit, expected release commit $GITHUB_SHA"
  exit 1
fi
