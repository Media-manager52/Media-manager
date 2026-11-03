// Package architecture holds the repository's architecture checks.
//
// It contains no production code. Its tests read the import graph and file
// sizes of cmd/ and internal/ and fail when a change crosses a layer
// boundary or grows an oversized file. Known violations are listed in
// allowlist.go; the lists may only shrink: a listed violation that no
// longer exists also fails the test, so the fix removes it from the list.
// See docs/architecture/boundaries.md for the rules and the reasons.
package architecture
