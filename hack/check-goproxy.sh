#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Asserts that Go module resolution is routed through the DGXC Artifactory
# proxy. Run it immediately after .github/actions/setup-dgxc-goproxy.
#
# GOPROXY is an ordered list consulted left to right, so an entry sitting ahead
# of Artifactory routes nothing while every downstream step still reports a
# healthy posture. If the action ever no-ops, only a failing assertion catches
# it; a build cannot.

set -euo pipefail

readonly ARTIFACTORY_PREFIX="https://edge.urm.nvidia.com/"

proxy="$(go env GOPROXY)"
echo "GOPROXY=${proxy}"

# Unlike a comma, the pipe separator falls through on any error including 401
# and 403, so a rejected credential would silently resolve from the public
# internet instead of failing.
case "${proxy}" in
  *"|"*)
    echo "ERROR: GOPROXY uses '|', which falls through on 401/403. Use ','." >&2
    exit 1
    ;;
esac

# Artifactory must be first. A trailing `,direct` is the `routed` posture and is
# expected; `enforced` drops it.
case "${proxy%%,*}" in
  "${ARTIFACTORY_PREFIX}"*) ;;
  *)
    echo "ERROR: first GOPROXY entry is '${proxy%%,*}', not Artifactory" >&2
    exit 1
    ;;
esac

if [ "$(go env GOSUMDB)" = off ]; then
  echo "ERROR: GOSUMDB=off; modules would not be verified against the transparency log" >&2
  exit 1
fi

echo "OK: Artifactory is first in GOPROXY and GOSUMDB is intact"
