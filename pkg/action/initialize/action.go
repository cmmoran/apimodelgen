package initialize

import (
	"os"
	"path"

	"github.com/cmmoran/apimodelgen/pkg/parser"
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
	ff, err := os.OpenFile(outFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		panic(err)
	}
	err = f.Render(ff)
	if err != nil {
		panic(err)
	}
	_ = ff.Close()
}
