// AnySave single-file Windows Setup launcher (AnySave-Setup.exe).
//
// Reads the embedded/appended ZIP payload from the end of its own executable,
// extracts it to a temporary directory, and executes `install.ps1`.
package main

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const trailerMagic = "ANYSAVE1"

func main() {
	self, err := os.Executable()
	if err != nil {
		fatal("Could not locate installer executable: " + err.Error())
	}
	f, err := os.Open(self)
	if err != nil {
		fatal("Could not open installer executable: " + err.Error())
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.Size() < 24 {
		fatal("Invalid installer image.")
	}

	var trailer [16]byte
	if _, err := f.ReadAt(trailer[:], st.Size()-16); err != nil {
		fatal("Could not read installer trailer: " + err.Error())
	}
	if string(trailer[8:16]) != trailerMagic {
		fatal("This AnySave-Setup.exe does not contain an embedded payload. Run scripts/build.sh to package it.")
	}
	zipSize := int64(binary.LittleEndian.Uint64(trailer[0:8]))
	zipOffset := st.Size() - 16 - zipSize
	if zipSize <= 0 || zipOffset <= 0 {
		fatal("Corrupted installer payload offset.")
	}

	zr, err := zip.NewReader(io.NewSectionReader(f, zipOffset, zipSize), zipSize)
	if err != nil {
		fatal("Could not read embedded archive: " + err.Error())
	}

	tmpDir, err := os.MkdirTemp("", "anysave-setup-*")
	if err != nil {
		fatal("Could not create temporary folder: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	for _, zf := range zr.File {
		rel := filepath.Clean(zf.Name)
		if strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(tmpDir, rel)
		if zf.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		rc, err := zf.Open()
		if err != nil {
			fatal("Could not extract " + zf.Name + ": " + err.Error())
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			fatal("Could not write " + zf.Name + ": " + err.Error())
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			fatal("Failed extracting " + zf.Name + ": " + err.Error())
		}
	}

	scriptPath := filepath.Join(tmpDir, "install.ps1")
	if _, err := os.Stat(scriptPath); err != nil {
		// Check one directory level down (e.g. AnySave-windows/install.ps1)
		alt := filepath.Join(tmpDir, "AnySave-windows", "install.ps1")
		if _, err2 := os.Stat(alt); err2 == nil {
			scriptPath = alt
		}
	}

	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "Error:", msg)
	os.Exit(1)
}

