# bifrost-cliproxyapi-metadata

Native Bifrost plugin that enriches `cliproxyapi/*` models with metadata from CLIProxyAPI's extended Pi catalog.

Bifrost still decides which models are available. This plugin only adds metadata such as context size, output limits, input modalities, display name, description, and reasoning efforts.

```text
Pi / OMP / other clients
        ↓
      Bifrost
        ↓
    CLIProxyAPI
```

## Compatibility

Current build target:

* Bifrost `v2.2.0` dynamic image revision `v2.2.0-r1`
* Bifrost core `v1.9.0`
* Go `1.27.0`
* Linux `amd64` / Alpine `musl`

The supported runtime image is:

```text
ghcr.io/fipnooone/bifrost-dynamic:v2.2.0-r1
```

For reproducible deployment, pin the published digest:

```text
ghcr.io/fipnooone/bifrost-dynamic@sha256:482d7897084eb9a7e2f0f7cc105e605013dde2929598897646afb5df54dbc754
```

The official `maximhq/bifrost` image is statically linked and is not a supported
runtime for this native Go plugin. The dynamic image is an unofficial Bifrost
build that preserves the pinned Bifrost source and toolchain while enabling
native plugin loading.

Go plugins must be built against a compatible Bifrost/Go toolchain. Do not assume a `.so` built for one Bifrost release will work with another.

## Configuration

Set the CLIProxyAPI token in the Bifrost runtime environment:

```bash
CLIPROXYAPI_API_KEY=...
```

Plugin config:

```json
{
  "provider_id": "cliproxyapi",
  "base_url": "https://cli-proxy-api.example.com",
  "api_key_env": "CLIPROXYAPI_API_KEY",
  "timeout_ms": 5000
}
```

The plugin reads metadata from:

```text
GET /v1/models?client_version=pi
```

and enriches matching models returned by Bifrost's normal `/v1/models` flow.

## Bifrost setup

Use an immutable GitHub Release URL for the plugin:

```json
{
  "plugins": [
    {
      "name": "cliproxyapi-model-metadata",
      "enabled": true,
      "path": "https://github.com/<owner>/bifrost-cliproxyapi-metadata/releases/download/v0.1.0/bifrost-cliproxyapi-metadata-v0.1.0-bifrost-v2.2.0-linux-amd64.so",
      "config": {
        "provider_id": "cliproxyapi",
        "base_url": "https://cli-proxy-api.example.com",
        "api_key_env": "CLIPROXYAPI_API_KEY",
        "timeout_ms": 5000
      }
    }
  ]
}
```

## Metadata mapping

| CLIProxyAPI                           | Bifrost                         |
| ------------------------------------- | ------------------------------- |
| `display_name`                        | `name`                          |
| `description`                         | `description`                   |
| `context_window`                      | `context_length`                |
| `max_tokens`                          | `max_output_tokens`             |
| `input_modalities`                    | `architecture.input_modalities` |
| `supported_reasoning_levels[].effort` | `reasoning.supported_efforts`   |
| `default_reasoning_level`             | `reasoning.default_effort`      |

Only exact model IDs are matched. The plugin never adds or removes models.

If the metadata request fails, the last successful in-memory catalog is used. If no cache exists, Bifrost's original response is returned unchanged.

## Verify

```bash
curl -s \
  -H "Authorization: Bearer $BIFROST_API_KEY" \
  "https://<bifrost>/v1/models" \
  | jq '.data[] | select(.id == "cliproxyapi/gpt-5.6-sol")'
```

Expected fields include:

```json
{
  "id": "cliproxyapi/gpt-5.6-sol",
  "name": "GPT 5.6 Sol",
  "context_length": 272000,
  "max_output_tokens": 128000,
  "architecture": {
    "input_modalities": ["text", "image"]
  },
  "reasoning": {
    "supported_efforts": ["low", "medium", "high", "xhigh", "max", "ultra"],
    "default_effort": "low"
  }
}
```

## Development

```bash
make test
make test-race
make build
```

Build output:

```text
dist/bifrost-cliproxyapi-metadata.so
```

Tags like `v0.1.0` trigger the GitHub Actions release workflow, which runs tests, builds the plugin, generates SHA-256 checksums, and publishes a versioned release asset.

## License

Add your preferred license before publishing the repository.
