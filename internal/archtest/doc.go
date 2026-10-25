// Package archtest holds the tests that keep the architecture honest: they read the import graph of
// internal/ and fail when a layer depends on one it must not (AD-025). It has no code to import.
package archtest
