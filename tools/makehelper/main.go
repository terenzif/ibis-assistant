package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: makehelper <build|build-mcp|dist|clean|test|lint>")
	}

	var err error
	switch os.Args[1] {
	case "build":
		err = buildServer()
	case "build-mcp":
		err = buildMCPBridge()
	case "dist":
		err = dist()
	case "clean":
		err = clean()
	case "test":
		err = test()
	case "lint":
		err = lint()
	default:
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func buildServer() error {
	return run("go", "build", "-o", exeName("ibis-assistant"), "./cmd/server")
}

func buildMCPBridge() error {
	return run("go", "build", "-o", exeName("mcp-bridge"), "./tools/mcp-bridge")
}

func dist() error {
	if err := os.MkdirAll("dist", 0o755); err != nil {
		return err
	}

	if err := moveFile(exeName("ibis-assistant"), filepath.Join("dist", exeName("ibis-assistant"))); err != nil {
		return err
	}
	if err := moveFile(exeName("mcp-bridge"), filepath.Join("dist", exeName("mcp-bridge"))); err != nil {
		return err
	}

	if err := os.RemoveAll(filepath.Join("dist", "rules")); err != nil {
		return err
	}
	if err := copyDir("rules", filepath.Join("dist", "rules")); err != nil {
		return err
	}

	if err := copyFile("sgconfig.yml", filepath.Join("dist", "sgconfig.yml")); err != nil {
		return err
	}
	if err := copyFile("config_master.json", filepath.Join("dist", "config.json")); err != nil {
		return err
	}

	return nil
}

func clean() error {
	for _, p := range []string{
		exeName("ibis-assistant"),
		exeName("mcp-bridge"),
		filepath.Join("dist", exeName("ibis-assistant")),
		filepath.Join("dist", exeName("mcp-bridge")),
		filepath.Join("dist", "config.json"),
		filepath.Join("dist", "sg.exe"),
		filepath.Join("dist", "surreal.exe"),
		filepath.Join("dist", "sg"),
		filepath.Join("dist", "surreal"),
	} {
		if err := removeFileIfExists(p); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(filepath.Join("dist", "rules")); err != nil {
		return err
	}
	return nil
}

func test() error {
	return run("go", "test", "./cmd/...", "./internal/...", "./tools/...")
}

func lint() error {
	return run("go", "vet", "./cmd/...", "./internal/...", "./tools/...")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrInvalid) {
		if copyErr := copyFile(src, dst); copyErr != nil {
			return err
		}
		return os.Remove(src)
	}

	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func fatalf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
