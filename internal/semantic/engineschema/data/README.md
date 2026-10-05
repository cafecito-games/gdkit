# Embedded Godot engine schema

`godot_4_7.json.gz` is generated from the unmodified `extension_api.json`
dump produced by Godot 4.7.2 stable official (`ed1daf0bf`). Its raw SHA-256 is
`d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986`.
The artifact's semantic schema SHA-256 is
`2b38249d74e48221e7fcc655592929e5c1bf4ab8d34c5c8b98ce8655a18c0a66`.

Regenerate it from a verified official dump with:

```sh
go run ./internal/semantic/engineschema/cmd/generate \
  -input /path/to/extension_api.json \
  -output internal/semantic/engineschema/data/godot_4_7.json.gz \
  -source-commit ed1daf0bf \
  -raw-sha256 d0e4c08c03b165156dabe6bfb6a906baf0069189f62035341230a246c86d6986
```

The retained API facts are derived from Godot Engine and distributed under
Godot's MIT license in `GODOT_LICENSE.txt`.

`../testdata/extension_api_4_7_2_official.json.gz` is a normalized gzip wrapper
around the exact 6,965,057 producer bytes used above, retained so the raw
loader and generator are exercised byte-for-byte. The wrapper's SHA-256 is
`26cc942ddf4b0ac936218f39bf54572cda2962d5f4c78ceb70c29ff4a92d891c`.
