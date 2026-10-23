# Embedded Godot engine schema

`godot_4_7.json.gz` is generated from the unmodified `extension_api.json`
dump produced by Godot 4.7.2 stable official (`ed1daf0bf`). Its raw SHA-256 is
`d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986`.
The artifact's semantic schema SHA-256 is
`7f78a5c8436cc33f38e1491c8e33c6885f49b71c7cde92cd528b95be70285fc6`.
The committed gzip is 206,658 bytes with SHA-256
`49363e14e1f8d5f0c82d0ebf89fcb97911d7acb0ae6f0c583e94311ac75bf6a2`.

Regenerate it from a verified official dump with:

```sh
go run ./internal/semantic/engineschema/cmd/generate \
  -input /path/to/extension_api.json \
  -output internal/semantic/engineschema/data/godot_4_7.json.gz \
  -source-commit ed1daf0bf \
  -raw-sha256 d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986
```

The command verifies the generated artifact before replacing the destination
and prints `raw_sha256`, `schema_sha256`, `artifact_sha256`, and
`artifact_bytes` for the registry and provenance records.

When advancing the bundled patch or adding a forward minor, update these as one
reviewed provenance change:

1. Verify the official dump's header, byte count, and raw digest, then replace
   the exact producer fixture in `../testdata`.
2. Generate the artifact and record every printed digest and size.
3. Update the exact registry key/artifact and its pinned `registryIdentity` in
   `../registry.go`; add a key for a new minor rather than replacing or falling
   back to another minor.
4. Update the provenance constants and deterministic-regeneration assertions in
   `../schema_test.go`.
5. Update this file, the root `README.md`, and the refresh command/provenance
   guidance in `CLAUDE.md` together.
6. Run the focused engine-schema tests, the full race suite, and the GoReleaser
   snapshot/archive-attribution checks before committing the artifact.

The retained API facts are derived from Godot Engine and distributed under
Godot's MIT license in `GODOT_LICENSE.txt`.

`../testdata/extension_api_4_7_2_official.json.gz` is a normalized gzip wrapper
around the exact 6,965,057 producer bytes used above, retained so the raw
loader and generator are exercised byte-for-byte. The wrapper's SHA-256 is
`26cc942ddf4b0ac936218f39bf54572cda2962d5f4c78ceb70c29ff4a92d891c`.
