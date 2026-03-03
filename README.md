# go-dmxcast [![Go Reference](https://pkg.go.dev/badge/github.com/bstkhq/go-dmxcast.svg)](https://pkg.go.dev/github.com/bstkhq/go-dmxcast)

Play OLA DMX show files and stream them over network protocols (currently Art-Net).

This project includes:
- `dmxcast`: a player that can run multiple shows at the same time and merge them (HTP/LTP).
- `olashow`: a parser/writer for OLA Show text files.
- `cmd/ola_player`: a CLI to play a show over Art-Net unicast.

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


## OLA show format

Standard OLA show header:

```
OLA Show
<universe> <v1>,<v2>,...,<vn>
<delay_ms>
...
```

This repo also supports optional metadata lines at the beginning:

```
# name=My Show
# loop=true
OLA Show
...
```

- Only `name` and `loop` are supported.
- `# ...` lines are only allowed before `OLA Show`.


## License

MIT, see [LICENSE](LICENSE)