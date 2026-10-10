package vmware

import (
	"fmt"
	"path"
)

func vmwareCloneCommand(src, dstDir, dst, name string) string {
	return fmt.Sprintf(`set -eu
src=%s
srcdir=%s
dst=%s
dir=%s
test -f "$src"
mkdir -p %s
# mkdir is atomic: a duplicate attempt must never clear an existing directory.
mkdir "$dir" || { echo 'VMware target directory already exists; preserving it' >&2; exit 1; }
if vmrun clone "$src" "$dst" full -cloneName=%s; then exit 0; fi
# Fall back only inside the directory reserved by this command. Copying an
# ancestor into its child would recursively copy other workloads as well.
source_dir=$(cd "$srcdir" && pwd -P)
target_dir=$(cd "$dir" && pwd -P)
case "$target_dir/" in "$source_dir/"*) echo 'Unsafe VMware template directory' >&2; exit 1;; esac
cp -a "$srcdir"/. "$dir"/
original="$dir/$(basename "$src")"
test -f "$original"
if [ "$original" != "$dst" ]; then mv "$original" "$dst"; fi
`, shellQuote(src), shellQuote(path.Dir(src)), shellQuote(dst), shellQuote(dstDir), shellQuote(path.Dir(dstDir)), shellQuote(name))
}
