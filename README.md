# tfstate-diff

Compare terraform environments.

## Usage

### GitHub Actions

This repository provides the app as a custom Github action.

To run manually:

```yaml
name: tfstate-diff
on:
  workflow_dispatch:
    inputs:
      plan:
        description: Compare plans (.tf files)
        type: boolean
        required: false
        default: true
jobs:
  diff:
    name: 'tfstate-diff'
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: write
    steps:
      - uses: actions/checkout@v2
      - name: Configure AWS Credentials
        uses: aws-actions/configure-aws-credentials@v1
        with:
          role-to-assume: arn:aws:iam::000000000000:role/something-for-terraform
          aws-region: ap-northeast-1
      - uses: HASHIMOTO-Takafumi/tfstate-diff@v1
        with:
          left-directory: ./terraform/left
          right-directory: ./terraform/right
          plan: ${{ inputs.plan }}
          # You can create following files and specify it
          # config: ./.github/config.yaml
          # template: ./.github/template.yaml
```

### Command line

```sh
## At left terraform directory
# Output state to compare
$ terraform show -json > state.json

# Output schema
$ terraform providers schema -json > schema.json

## At right terraform directory
## You can also use plan
$ terraform plan -out plan
$ terraform show -json plan > plan.json

## At somewhere config.yaml exists
$ tfstate-diff -c config.yaml left/schema.json left/state.json right/plan.json

common resources:       100
resources with diff:     33
left only resources:     22
right only resources:    11
```

Use the verbose option `-v` to inspect diffs.
