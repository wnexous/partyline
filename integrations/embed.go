// Package integrations embeds the extension and plugin files `partyline setup` installs into
// oh-my-pi (omp), pi and OpenCode (HAR-01). The TypeScript sources live next to this file.
package integrations

import "embed"

// Files holds omp/partyline.ts, pi/partyline.ts and opencode/partyline.ts.
//
//go:embed omp/partyline.ts pi/partyline.ts opencode/partyline.ts
var Files embed.FS
