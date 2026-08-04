# cloud-skills-mcp plugin

Codex plugin bundle for the six cloud skills (AWS, Azure, Google Cloud,
Alibaba Cloud, Tencent Cloud, Baidu AI Cloud). The skills are instructions for
driving the `cloud-skills-mcp` stdio MCP server; credentials never enter MCP
arguments and are resolved lazily from each provider's official identity
chain in the server process.

## Install from this repository as a marketplace

```bash
codex plugin marketplace add https://github.com/tttboy123/cloud-skills-mcp.git
codex plugin add cloud-skills-mcp@cloud-skills-mcp
```

Then build and register the MCP server binary (see the repository README):

```bash
./install.sh --bin-dir ~/.local/bin
```

and add the binary as an `mcp_servers.cloud-skills-mcp` entry in
`~/.codex/config.toml` with `command = "cloud-skills-mcp"`.

The plugin bundle is verified by `scripts/ci/mapping-audit.sh`, which fails if
`plugin/skills/<provider>` drifts from the canonical `<provider>` skill.
