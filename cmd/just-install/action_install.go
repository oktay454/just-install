// just-install - The simple package installer for Windows
// Copyright (C) 2020 just-install authors.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, version 3 of the License.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gotopkg/mslnk/pkg/mslnk"
	"github.com/ungerik/go-dry"
	"github.com/urfave/cli/v2"

	"github.com/just-install/just-install/pkg/cmd"
	"github.com/just-install/just-install/pkg/fetch"
	"github.com/just-install/just-install/pkg/installer"
	"github.com/just-install/just-install/pkg/paths"
	"github.com/just-install/just-install/pkg/platform"
	"github.com/just-install/just-install/pkg/registry4"
	"github.com/just-install/just-install/pkg/strings2"
)

var (
	shimsPath = os.ExpandEnv("${SystemDrive}\\Shims")
	startMenu = os.ExpandEnv("${ProgramData}\\Microsoft\\Windows\\Start Menu\\Programs")
)

func handleInstall(c *cli.Context) error {
	force := c.Bool("force")
	onlyDownload := c.Bool("download-only")
	onlyShims := c.Bool("shim")

	registry, err := loadRegistry(c, force)
	if err != nil {
		return err
	}

	arch, err := getInstallArch(c.String("arch"))
	if err != nil {
		return err
	}

	printInteractivePackages(registry.Packages, c.Args().Slice())

	// Install packages
	hasErrors := false

	for _, pkg := range c.Args().Slice() {
		entry, ok := registry.Packages[pkg]
		if !ok {
			log.Println("WARNING: unknown package", pkg)
			continue
		}

		options, err := entry.Installer.OptionsForArch(arch)
		if err != nil {
			return err
		}

		if onlyShims {
			mustCreateShims(options.Shims, entry.Version)
			continue
		}

		installerPath, err := fetchInstaller(entry, arch, force)
		if err != nil {
			log.Printf("error downloading %v: %v", pkg, err)
			hasErrors = true
			continue
		}

		if onlyDownload {
			continue
		}

		installerPath, err = maybeExtractContainer(installerPath, options)
		if err != nil {
			return err
		}

		if err := install(installerPath, entry.Installer.Kind, options); err != nil {
			log.Printf("error installing %v: %v", pkg, err)
			hasErrors = true
			continue
		}

		if len(options.Shims) > 0 {
			if err := createShims(options.Shims, entry.Version); err != nil {
				log.Printf("could not create shims for: %v due to %v", pkg, err)
				hasErrors = true
				continue
			}
		}

	}

	if hasErrors {
		return errors.New("encountered errors installing packages (see the log for details)")
	}

	return nil
}

// getInstallArch returns the architecture selected for package installation based on the given
// preferred architecture (e.g. given by the user via command line arguments). The given preferred
// architecture can be empty, in which case a suitable one is automatically selected for the current
// machine.
func getInstallArch(preferredArch string) (string, error) {
	switch preferredArch {
	case "":
		if platform.Is64Bit() {
			return "x86_64", nil
		}

		return "x86", nil
	case "x86":
		return preferredArch, nil
	case "x86_64":
		if !platform.Is64Bit() {
			return "", errors.New("this machine cannot run 64-bit software")
		}

		return preferredArch, nil
	default:
		return "", fmt.Errorf("unknown architecture: %v", preferredArch)
	}
}

// printInteractivePackages prints the names of packages that require user interaction.
func printInteractivePackages(packageMap registry4.PackageMap, requestedPackages []string) {
	var interactive []string

	for _, pkg := range requestedPackages {
		entry, ok := packageMap[pkg]
		if !ok {
			continue
		}

		if entry.Installer.Interactive {
			interactive = append(interactive, pkg)
		}
	}

	if len(interactive) < 1 {
		return
	}

	log.Println("these packages might require user interaction to complete their installation")

	for _, pkg := range interactive {
		log.Println("    " + pkg)
	}

	log.Println("")
}

// fetchInstaller fetches the installer for the given package and returns
func fetchInstaller(entry *registry4.Package, arch string, overwrite bool) (string, error) {
	// Sanity check
	if isEmptyString(entry.Installer.X86) && isEmptyString(entry.Installer.X86_64) {
		return "", errors.New("package entry is missing both 32-bit and 64-bit installers")
	}

	// Pick preferred installer
	var installerURL string
	switch arch {
	case "x86":
		if isEmptyString(entry.Installer.X86) {
			return "", errors.New("this package doesn't offer a 32-bit installer")
		}

		installerURL = entry.Installer.X86
	case "x86_64":
		if isEmptyString(entry.Installer.X86_64) {
			// Fallback to the 32-bit installer
			installerURL = entry.Installer.X86
		} else {
			installerURL = entry.Installer.X86_64
		}
	default:
		panic("programmer error")
	}

	installerURL, err := expandString(installerURL, map[string]string{"version": entry.Version})
	if err != nil {
		return "", fmt.Errorf("could not expand installer URL's template string: %w", err)
	}

	downloadDir, err := paths.TempDirCreate()
	if err != nil {
		return "", fmt.Errorf("could not create temporary directory to download installer: %w", err)
	}

	ret, err := fetch.Fetch(installerURL, &fetch.Options{
		Destination: downloadDir,
		Overwrite:   overwrite,
		Progress:    true,
	})

	return ret, err
}

func maybeExtractContainer(path string, options *registry4.Options) (string, error) {
	if options == nil || options.Container == nil {
		return path, nil
	}

	if strings2.IsEmpty(options.Container.Installer) {
		return "", errors.New("\"installer\" option cannot be empty")
	}

	if options.Container.Kind != "zip" {
		return "", errors.New("only \"zip\" containers are supported")
	}

	tempDir, err := paths.TempDirCreate()
	if err != nil {
		return "", err
	}

	extractDir := filepath.Join(tempDir, filepath.Base(path)+"_extracted")
	log.Println("extracting container", path, "to", extractDir)
	if err := installer.ExtractZIP(path, extractDir); err != nil {
		return "", err
	}

	return filepath.Join(extractDir, options.Container.Installer), nil
}

func install(path string, kind string, options *registry4.Options) error {
	// One-off, custom, installers
	switch kind {
	case "copy":
		if options == nil {
			return errors.New("the \"copy\" installer requires additional options")
		}

		if strings2.IsEmpty(options.Destination) {
			return errors.New("\"destination\" is missing from installer options")
		}

		destination, err := expandString(options.Destination, nil)
		if err != nil {
			return fmt.Errorf("could not expand destination string: %w", err)
		}

		parentDir := filepath.Dir(destination)
		log.Println("creating", parentDir)
		if err := os.MkdirAll(parentDir, os.ModePerm); err != nil {
			return err
		}

		log.Println("copying to", destination)
		return dry.FileCopy(path, destination)
	case "custom":
		if options == nil {
			return errors.New("the \"custom\" installer requires additional options")
		}

		if len(options.Arguments) < 1 {
			return errors.New("\"arguments\" is missing from installer options")
		}

		var args []string
		for _, v := range options.Arguments {
			expanded, err := expandString(v, map[string]string{"installer": path})
			if err != nil {
				return err
			}

			args = append(args, expanded)
		}

		return cmd.Run(args...)
	case "zip":
		if options == nil {
			return errors.New("the \"zip\" installer requires additional options")
		}

		if strings2.IsEmpty(options.Destination) {
			return errors.New("\"destination\" is missing from installer options")
		}

		destination, err := expandString(options.Destination, nil)
		if err != nil {
			return fmt.Errorf("could not expand destination string: %w", err)
		}

		log.Println("extracting to", destination)
		if err := installer.ExtractZIP(path, destination); err != nil {
			return err
		}

		for _, shortcut := range options.Shortcuts {
			shortcutName, err := expandString(shortcut.Name, nil)
			if err != nil {
				return fmt.Errorf("could not expand shortcut name string template: %w", err)
			}

			shortcutTarget, err := expandString(shortcut.Target, nil)
			if err != nil {
				return fmt.Errorf("could not expand shortcut target string template: %w", err)
			}

			shortcutLocation := filepath.Join(startMenu, shortcutName+".lnk")

			log.Println("creating shortcut to", shortcutTarget, "in", shortcutLocation)
			if err := mslnk.LinkFile(shortcutTarget, shortcutLocation); err != nil {
				return fmt.Errorf("could not create shortcut: %w", err)
			}
		}

		return nil
	}

	// Regular installer
	installerType := installer.InstallerType(kind)
	if !installerType.IsValid() {
		return fmt.Errorf("unknown installer type: %v", kind)
	}

	installerCommand, err := installer.Command(path, installerType)
	if err != nil {
		return err
	}

	return cmd.Run(installerCommand...)
}

func isEmptyString(s string) bool {
	return len(strings.TrimSpace(s)) < 1
}

// expandString expands any environment variable in the given string, with additional variables
// coming from the given context.
func expandString(s string, context map[string]string) (string, error) {
	data := environMap()

	// Merge the given context
	for k, v := range context {
		data[k] = v
	}

	var buf bytes.Buffer
	t, err := template.New("expandString").Parse(s)
	if err != nil {
		return "", err
	}
	t.Execute(&buf, data)

	return buf.String(), nil
}

// environMap returns the current environment variables as a map.
func environMap() map[string]string {
	ret := make(map[string]string)
	env := os.Environ()

	for _, v := range env {
		split := strings.SplitN(v, "=", 2)

		if split[0] == "" && split[1] == "" {
			continue
		}

		split[0] = strings.ToUpper(split[0]) // Normalize variable names to upper case
		split[0] = strings.Replace(split[0], "(X86)", "_X86", -1)

		ret[split[0]] = split[1]
	}

	return ret
}

// mustCreateShims calls createShims and aborts when it fails.
func mustCreateShims(shims []string, entryVersion string) {
	if err := createShims(shims, entryVersion); err != nil {
		log.Fatalln(err)
	}
}

// createShims tries to create the given shims using exeproxy, if it's installed.
func createShims(shims []string, entryVersion string) error {
	exeproxy := os.ExpandEnv("${ProgramFiles(x86)}\\exeproxy\\exeproxy.exe")
	if !dry.FileExists(exeproxy) {
		return errors.New("could not find exeproxy")
	}

	if !dry.FileIsDir(shimsPath) {
		if err := os.MkdirAll(shimsPath, 0); err != nil {
			return fmt.Errorf("could not create shims directory: %w", err)
		}
	}

	for _, v := range shims {
		shimTarget, err := expandString(v, nil)
		if err != nil {
			return fmt.Errorf("could not expand shim target string: %w", err)
		}

		shim := filepath.Join(shimsPath, filepath.Base(shimTarget))

		if dry.FileExists(shim) {
			if err := os.Remove(shim); err != nil {
				return fmt.Errorf("could not remove existing shim: %v, %w", shim, err)
			}
		}

		log.Printf("creating shim for %s (%s)\n", shimTarget, shim)

		if err := cmd.Run(exeproxy, "exeproxy-copy", shim, shimTarget); err != nil {
			return fmt.Errorf("could not create shim: %w", err)
		}
	}

	return nil
}
