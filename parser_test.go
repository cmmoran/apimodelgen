package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	. "github.com/cmmoran/apimodelgen/pkg/parser"
)

func TestParse(ttt *testing.T) {
	inDir := "test/testdata/fixtures/canonical"
	outDir := "test/testdata/fixtures/expectations"
	type args struct {
		opts []Option
	}
	tests := []struct {
		name    string
		args    args
		want1   map[string]string
		wantErr bool
	}{
		{
			name: "parse with defaults",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/api", outDir)),
				},
			},
			wantErr: false,
		},
		{
			name: "parse with includeEmbedded",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/includeembedded/api", outDir)),
					WithIncludeEmbedded(),
				},
			},
			wantErr: false,
		},
		{
			name: "parse with suffix=Out",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/suffix/api", outDir)),
					WithSuffix("Out"),
				},
			},
			wantErr: false,
		},
		{
			name: "parse with keepORMTags",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/keepormtags/api", outDir)),
					WithKeepORMTags(),
				},
			},
			wantErr: false,
		},
		{
			name: "parse with excludedeprecated",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/excludedeprecated/api", outDir)),
					WithExcludeDeprecated(),
				},
			},
			wantErr: false,
		},
		{
			name: "parse with excludetype",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/excludetype/api", outDir)),
					WithExcludeTypes("TestEmbedded"),
				},
			},
			wantErr: false,
		},
		{
			name: "parse with excludetags",
			args: args{
				opts: []Option{
					WithInDir(inDir),
					WithOutDir(fmt.Sprintf("%s/excludetag/api", outDir)),
					WithExcludeByTag("dto", "-"),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		ttt.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := &Options{
				FlattenEmbedded: true,
			}
			for _, fn := range tt.args.opts {
				fn(o)
			}
			jsbyt, _ := json.MarshalIndent(o, "", "  ")
			t.Logf("Options: %v", string(jsbyt))
			var (
				got *Parser
				err error
			)
			got, err = New(tt.args.opts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			err = got.Parse()
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			f := got.GenerateApiFile()
			expectedBytes, _ := os.ReadFile(filepath.Join(got.Opts.OutDir, got.Opts.OutFile))
			outBuf := new(bytes.Buffer)
			err = f.Render(outBuf)
			if err != nil {
				t.Errorf("Render() error = %v", err)
				return
			}
			diff := cmp.Diff(outBuf.String(), string(expectedBytes))
			if diff != "" {
				t.Logf("diff: %s", diff)
				t.Logf("expected: %s", outBuf.String())
				t.Logf("actual: %s", string(expectedBytes))
			}
			require.EqualValuesf(t, string(expectedBytes), outBuf.String(), "Render() got=%s, expected=%s, diff = %s", outBuf.String(), string(expectedBytes), diff)
		})
	}
}
