#!/bin/sh
# Prints the version to stamp into the partyline binary (ONB-11). The Dockerfile runs it in
# the build context; it works the same anywhere:
#   1. $VERSION, when set to anything but "dev";
#   2. `git describe --tags` of the .git in the current directory (in a Docker build: the
#      build context, which includes .git);
#   3. dev-<first 12 characters of the commit> of that .git when no tag is reachable (a
#      shallow clone, as PaaS builders make), else of $SOURCE_COMMIT;
#   4. dev.
set -eu

main() {
	if [ -n "${VERSION:-}" ] && [ "$VERSION" != dev ]; then
		printf '%s\n' "$VERSION"
		return
	fi
	if [ -e .git ] && command -v git >/dev/null 2>&1; then
		# safe.directory: the build may run as another user than the one owning the files.
		if v=$(git -c safe.directory='*' describe --tags 2>/dev/null); then
			printf '%s\n' "$v"
			return
		fi
		if c=$(git -c safe.directory='*' rev-parse HEAD 2>/dev/null); then
			printf 'dev-%.12s\n' "$c"
			return
		fi
	fi
	if [ -n "${SOURCE_COMMIT:-}" ]; then
		printf 'dev-%.12s\n' "$SOURCE_COMMIT"
		return
	fi
	echo dev
}

main
