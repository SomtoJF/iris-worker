package helper

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"text/template"
)

// LoadTemplate loads a template file from the given path with optional custom functions
func LoadTemplate(templatePath string) (*template.Template, error) {
	return LoadTemplateWithFuncs(templatePath, nil)
}

// LoadTemplateWithFuncs loads a template file from the given path with custom functions
func LoadTemplateWithFuncs(templatePath string, funcMap template.FuncMap) (*template.Template, error) {
	// Try to get the project root directory
	projectRoot := getProjectRoot()

	// Build the full path to the template
	fullPath := filepath.Join(projectRoot, templatePath)

	// Read the template file
	templateContent, err := ioutil.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file %s: %w", fullPath, err)
	}

	// Parse the template
	tmpl, err := template.New(filepath.Base(templatePath)).Funcs(funcMap).Parse(string(templateContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	return tmpl, nil
}

// getProjectRoot attempts to find the project root directory
func getProjectRoot() string {
	// First, check if WORKER_ROOT environment variable is set
	if root := os.Getenv("WORKER_ROOT"); root != "" {
		return root
	}

	// Otherwise, try to find the root based on the location of this file
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		// Go up from helper/templates.go to the project root
		dir := filepath.Dir(filename)
		projectRoot := filepath.Dir(dir) // Go up one level from helper/
		return projectRoot
	}

	// Fallback to current working directory
	pwd, _ := os.Getwd()
	return pwd
}
