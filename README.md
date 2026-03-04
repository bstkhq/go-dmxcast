# go-dmxcast [![Go Reference](https://pkg.go.dev/badge/github.com/bstkhq/go-dmxcast.svg)](https://pkg.go.dev/github.com/bstkhq/go-dmxcast)

Play OLA DMX show files and stream them over network protocols (currently Art-Net).

This project includes:
- `dmxcast`: a player that can run multiple shows at the same time and merge them (HTP/LTP).
- `olashow`: a parser/writer for OLA Show text files.
- `cmd/ola_player`: a CLI to play one or more shows over Art-Net unicast with merge/loop controls.
- `cmd/ola_recorder`: a CLI recorder that captures Art-Net ArtDMX into an OLA Show file.

## Install

```bash
go get github.com/bstkhq/go-dmxcast
```

## Library usage

### Parse a show

```go
show, err := olashow.Open("show.show")
if err != nil {
	// ...
}
```

### Play over Art-Net

```go
tx, err := dmxcast.NewArtNetTransport(&dmxcast.ArtNetConfig{
	DstIP:  net.ParseIP("10.0.100.49"),
	SrcIP:  net.ParseIP("10.0.100.5"), // optional
	Net:    0,
	SubUni: 204,
})
if err != nil {
	// ...
}
defer tx.Close()

player := dmxcast.NewPlayer(tx, &dmxcast.PlayerConfig{
	Mode:          dmxcast.MergeHTP,
	FlushInterval: 0, // defaults to 44 Hz
})
defer player.Close()

h := player.Play(context.Background(), show)

// ...
player.Stop(h)
```


## CLI Usage

Play a show file via Art-Net unicast.

```bash
go run ./cmd/ola_player \
 -file ./show.show \
 -ip 10.0.100.49 \
 -net 0 \
 -subuni 204
```

Common options:

- `-loop` / `-once`: override the show metadata `loop`.
- `-mode htp|ltp`: merge mode (default `htp`).
- `-hz 44`: output refresh rate (default 44 Hz).
- `-stats 1s`: print a frame counter periodically (set `0` to disable).


## OLA Show format

Standard OLA show header:

```text
OLA Show
<universe> <v1>,<v2>,...,<vn>
<delay_ms>
...
```

### Metadata

This repo supports optional metadata that can be provided in two ways:

#### Inline metadata (in the `.show` file)

Metadata lines must appear **only at the beginning** of the file and must be a **consecutive block** (no blank lines inside). Each line uses:

```text
# key=value
```

Example:

```text
# name=My Show
# loop=true
# exclusive=true
# include=intro.show
OLA Show
...
```



#### Sidecar metadata file (`.metadata`)

If the show file is `myshow.show`, the loader will also look for:

```text
myshow.show.metadata
```

The sidecar uses the **same keys** but **without** the leading `#`:

```text
name=My Show
loop=3
exclusive=false
include=intro.show
```


### Supported keys

- `name=<string>`
  Optional display name for the show.

- `loop=<bool|int>`
  Controls how many times the show should repeat:
  - `true`  → infinite loop (`Loop = -1`)
  - `false` → play once (`Loop = 0`)
  - `<int>` → repeat that many times (`Loop = <int>`)

- `exclusive=<bool>`
  Indicates the show requests exclusive control while playing (the player should stop other running shows before starting this one).

- `include=<file.show>`
  Prepends the frames of another show before the current one.
  Can be specified multiple times; includes are applied in order.


## License

MIT, see [LICENSE](LICENSE)