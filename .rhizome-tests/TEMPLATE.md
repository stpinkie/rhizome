# Rhizome Test Report

## Environment

| Field | Value |
|---|---|
| Agent | {{AGENT_NAME}} |
| OS | {{OS}} |
| Architecture | {{ARCH}} |
| Go version | {{GO_VERSION}} |
| Node version | {{NODE_VERSION}} |
| pnpm version | {{PNPM_VERSION}} |
| Commit | {{COMMIT_SHA}} |
| Date | {{DATE}} |
| Duration | {{DURATION_SECONDS}} seconds |

## Commands Run

| Step | Command | Exit Code | Duration | Notes |
|---|---|---|---|---|
| {{STEP_1}} | `{{COMMAND_1}}` | {{EXIT_1}} | {{DUR_1}} | {{NOTES_1}} |
| {{STEP_2}} | `{{COMMAND_2}}` | {{EXIT_2}} | {{DUR_2}} | {{NOTES_2}} |

## Results

- Passed packages: {{PASSED_COUNT}}
- Failed packages: {{FAILED_COUNT}}
- Skipped packages: {{SKIPPED_COUNT}}

### Failed packages

{{FAILED_LIST}}

### Skipped packages / known platform skips

{{SKIPPED_LIST}}

## Summary

{{SUMMARY}}

## Notes

{{NOTES}}
