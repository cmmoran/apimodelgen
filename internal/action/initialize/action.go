package initialize

import (
	"os"
	"path"

	"github.com/cmmoran/apimodelgen/internal/parser"
)

func Generate(p *parser.Options) {
	par, err := parser.NewWithOpts(p)
	if err != nil {
		panic(err)
	}
	if err = par.Parse(); err != nil {
		panic(err)
	}
	f := par.GenerateApiFile()
	_ = os.MkdirAll(par.Opts.OutDir, 0755)
	outFile := path.Clean(par.Opts.OutDir + "/" + par.Opts.OutFile)
	ff, _ := os.OpenFile(outFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	err = f.Render(ff)
	if err != nil {
		panic(err)
	}
	_ = ff.Close()
}
