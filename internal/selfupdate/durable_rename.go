package selfupdate

// durableRename is a narrow package-test seam. Every recovery-authority path
// uses it instead of os.Rename so tests can verify replace/no-replace intent.
var durableRename = platformDurableRename
