/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/thekrauss/kubemanager/tools/enumgen/pkg"
)

var rootCmd = &cobra.Command{
	Use:   "enumgen",
	Short: "Generate code for enums",
	Long:  `Generate structs and code for enums`,
	Run:   GenerateEnums,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

type config struct {
	Package string
	Enums   []enum
	Path    string
}

type enum struct {
	Name         string
	Plural       string
	Values       map[string]string
	AllowUnknown bool
	Filename     string
}

var C []config

func readAndMergeConfigs(baseDir string) error {
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Base(path) == "enums.json" {
			bytes, err := os.ReadFile(path)
			cobra.CheckErr(err)

			var localConfig config
			err = json.Unmarshal(bytes, &localConfig)
			if err != nil {
				return err
			}

			dirConfig := config{
				Path:    filepath.Dir(path),
				Package: localConfig.Package,
				Enums:   make([]enum, 0, len(localConfig.Enums)),
			}

			for _, enum := range localConfig.Enums {
				enum.Filename = dirConfig.Path + "/" + enum.Filename
				dirConfig.Enums = append(dirConfig.Enums, enum)
			}
			C = append(C, dirConfig)
		}

		return nil
	})

	return err
}

type tmplValues struct {
	Values         map[string]string
	Type           string
	TypePlural     string
	TypePluralLC   string
	Package        string
	JSONSerializer bool
	TextSerializer bool
	DBSerializer   bool
	Unknown        bool
}

func GenerateEnums(cmd *cobra.Command, args []string) {
	err := readAndMergeConfigs(".")
	if err != nil {
		log.Fatal(err)
	}

	tmpl, err := template.New("const").Parse(pkg.Template)
	if err != nil {
		log.Fatal(err)
	}

	var group errgroup.Group

	for _, conf := range C {
		cfg := conf

		group.Go(func() error {
			for _, enum := range cfg.Enums {
				filename := fmt.Sprintf("%s/v%s_generated.go", cfg.Path, enum.Name)

				f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
				cobra.CheckErr(err)
				defer f.Close()

				t := tmplValues{
					Values:         enum.Values,
					Type:           enum.Name,
					TypePlural:     enum.Plural,
					TypePluralLC:   strings.ToLower(enum.Plural[0:1]) + enum.Plural[1:],
					Package:        cfg.Package,
					JSONSerializer: true,
					TextSerializer: true,
					Unknown:        enum.AllowUnknown,
					DBSerializer:   false,
				}

				if err := tmpl.Execute(f, t); err != nil {
					log.Fatal(err)
				}
			}
			return nil
		})
	}

	err = group.Wait()
	if err != nil {
		log.Fatal(err)
	}

	cmd2 := exec.Command("goimports", "-local", "github.com/thekrauss/kubemanager", "-w", ".")
	cmd2.Stdout = os.Stdout
	cmd2.Stderr = os.Stderr
	err = cmd2.Run()
	if err != nil {
		log.Fatal(err)
	}
}
