#!/usr/bin/env bash
# Copies infra/terraform/shared-variables.tf into every Terraform root.
# With --check, only reports copies that differ (used by CI).
set -euo pipefail
cd "$(dirname "$0")/../infra/terraform"
status=0
for root in gcp azure vps dns media-gcs media-azure; do
  if [ "${1:-}" = "--check" ]; then
    cmp -s shared-variables.tf "$root/shared-variables.tf" || { echo "$root/shared-variables.tf differs from shared-variables.tf: run scripts/sync-terraform-vars.sh"; status=1; }
  else
    rm -f "$root/shared-variables.tf"
    cp shared-variables.tf "$root/shared-variables.tf"
  fi
done
exit $status
