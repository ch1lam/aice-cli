# agent-browser provenance

Source: https://github.com/vercel-labs/agent-browser
Tag: v0.37.1
Commit: 72007a6788d863611b23bed0b59d0d659c638d8e
License: Apache-2.0 (see LICENSE).

AICE downloads the pinned native release binary and runs it as the MCP server
`agent-browser mcp`. Its upstream CLI skill documents are no longer vendored:
the MCP tools carry their own descriptions and server instructions.

On 2026-09-11 the four supported GitHub release binaries were downloaded and
compared byte-for-byte with the binaries in npm agent-browser@0.37.1.
All matched. Both downloads used this host's network; an independent second
network verification remains outstanding.
