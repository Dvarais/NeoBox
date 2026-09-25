// Not a real module: this file fences build/linux off from the NeoBox module.
//
// makepkg builds in place and leaves src/ and pkg/ here, and it locks pkg/ down
// to d--x--x--x. Go walks the whole module tree for ./... and `all`, so every
// go command after that — including Wails' binding generation — failed with
// "open build/linux/pkg: permission denied". Go never descends into a directory
// that has its own go.mod, so this keeps both out of the walk.
module neobox-linux-packaging

go 1.24
