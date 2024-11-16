//go:build cgo
// +build cgo

package dot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/goccy/go-graphviz"
)

func runDotToImage(outfname string, format string, dot []byte) (string, error) {
	g, err := graphviz.New(context.Background())
	if err != nil {
		return "", err
	}
	graph, err := graphviz.ParseBytes(dot)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := graph.Close(); err != nil {
			log.Fatal(err)
		}
		g.Close()
	}()
	var img string
	if outfname == "" {
		img = filepath.Join(os.TempDir(), fmt.Sprintf("go-callvis_export.%s", format))
	} else {
		img = fmt.Sprintf("%s.%s", outfname, format)
	}
	if err := g.RenderFilename(context.Background(), graph, graphviz.Format(format), img); err != nil {
		return "", err
	}
	return img, nil
}
