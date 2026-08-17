package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

func main() {
	preflight := flag.Bool("preflight", false, "run the authoritative side-effect-free SQLC preflight")
	verify := flag.Bool("verify", false, "verify generated SQLC files after generation")
	verifyShape := flag.Bool("verify-shape", false, "verify generated SQLC file shape without HEAD/index equality")
	manualExtensions := flag.Bool("manual-extensions", false, "print registered manual extension paths")
	gitBinary := flag.String("git-binary", "", "trusted absolute Git binary")
	flag.Parse()
	modeCount := boolInt(*preflight) + boolInt(*verify) + boolInt(*verifyShape) + boolInt(*manualExtensions)
	if flag.NArg() != 0 || modeCount > 1 {
		log.Fatal(errors.New("usage: go run ./scripts/sqlc_config_outputs [--git-binary ABSOLUTE] [--preflight|--verify|--verify-shape|--manual-extensions]"))
	}
	git, err := resolveGit(*gitBinary)
	if err != nil {
		log.Fatal(err)
	}
	units, err := loadUnits(*preflight, *verify, *verifyShape, git)
	if err != nil {
		log.Fatal(err)
	}
	if *verify || *verifyShape {
		return
	}
	printUnits(units, *manualExtensions)
}

func loadUnits(preflight, verify, verifyShape bool, git sqlcconfig.GitRunner) ([]sqlcconfig.Unit, error) {
	if preflight {
		return sqlcconfig.PreflightWithGit(context.Background(), ".", git)
	}
	units, err := sqlcconfig.LoadDefaultWithGit(context.Background(), ".", git)
	if err != nil {
		return nil, err
	}
	if verify {
		return units, sqlcconfig.VerifyGeneratedUnitsWithGit(context.Background(), ".", units, git)
	}
	if verifyShape {
		return units, sqlcconfig.VerifyGeneratedUnitsShapeWithGit(context.Background(), ".", units, git)
	}
	return units, nil
}

func printUnits(units []sqlcconfig.Unit, manualExtensions bool) {
	if manualExtensions {
		for _, relative := range sqlcconfig.ManualExtensionPaths(units) {
			fmt.Println(relative)
		}
		return
	}
	for _, unit := range units {
		fmt.Println(unit.Output)
	}
}

func resolveGit(binary string) (sqlcconfig.GitRunner, error) {
	if binary != "" {
		return sqlcconfig.NewGitRunner(binary)
	}
	return sqlcconfig.ResolveExternalGit(".")
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
