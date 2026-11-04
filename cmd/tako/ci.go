package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ciWorkflowTemplate = `name: Tako CI/CD

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read
  id-token: write

env:
  GOOGLE_CLOUD_PROJECT: %s
  TAKO_REGION: %s

jobs:
  verify-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.x'

      - name: Static analysis & code generation
        run: tako check

      - name: Run tests with generated harness
        run: tako test

      - name: Authenticate to Google Cloud (Workload Identity Federation)
        if: vars.GCP_WORKLOAD_IDENTITY_PROVIDER != ''
        uses: google-github-actions/auth@v2
        with:
          workload_identity_provider: ${{ vars.GCP_WORKLOAD_IDENTITY_PROVIDER }}
          service_account: ${{ vars.GCP_DEPLOY_SERVICE_ACCOUNT }}

      - name: Plan & version skew check (Pull Request)
        if: github.event_name == 'pull_request' && vars.GCP_WORKLOAD_IDENTITY_PROVIDER != ''
        run: tako deploy --plan

      - name: Deploy to production (main)
        if: github.event_name == 'push' && github.ref == 'refs/heads/main' && vars.GCP_WORKLOAD_IDENTITY_PROVIDER != ''
        run: tako deploy --yes
`

func runCI(raw []string) error {
	args := normalizeArgs(raw)
	sub := "init"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	if sub != "init" {
		return fmt.Errorf("usage: tako ci [init] [--project <id>] [--region <region>]")
	}

	project := "${{ vars.GOOGLE_CLOUD_PROJECT }}"
	region := "us-central1"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 >= len(args) {
				return fmt.Errorf("--project needs a value")
			}
			i++
			project = args[i]
		case "--region":
			if i+1 >= len(args) {
				return fmt.Errorf("--region needs a value")
			}
			i++
			region = args[i]
		default:
			return fmt.Errorf("unknown flag %q (tako ci init accepts --project, --region)", args[i])
		}
	}

	outPath := filepath.Join(".github", "workflows", "tako.yml")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(ciWorkflowTemplate, project, region)
	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Printf("tako ci · wrote %s\n", outPath)
	fmt.Println("  PRs run:   tako check -> tako test -> tako deploy --plan (with version & config skew check)")
	fmt.Println("  main runs: tako check -> tako test -> tako deploy --yes")
	return nil
}
