# drone-convert-to-harness

A Drone plugin to convert Drone YAML pipelines to Harness YAML format using the go-convert tool. This plugin wraps the go-convert CLI tool, making it easy to use within Drone pipelines.

## Features

- Converts Drone YAML to Harness YAML format
- Supports downgrading to legacy Harness YAML formats
- Configurable organization, project, and pipeline settings
- Cross-platform compatibility
- Uses the CI-17808 branch of go-convert

## Usage

```yaml
steps:
  - name: convert-to-harness
    image: harnesscommunity/drone-convert-to-harness
    settings:
      source_yaml: .drone.yml
      downgrade: true
```

## Build

Build the Docker image with:

```bash
docker build -f docker/Dockerfile-linux-amd64 -t harnesscommunity/drone-convert-to-harness .
```

## Environment Variables

### Inputs

| Environment Variable              | Description                                   | Default Value |
|-----------------------------------|-----------------------------------------------|---------------|
| `PLUGIN_CI_PROVIDER`              | CI provider type                              | `drone`       |
| `PLUGIN_SOURCE_YAML`              | Path to source YAML file                      |               |
| `PLUGIN_HARNESS_OUTPUT_YAML_PATH` | Output path for converted YAML                | `$DRONE_WORKSPACE/convert/harness_pipeline.yaml` |
| `PLUGIN_DOWNGRADE`                | Whether to downgrade the YAML                 | `true`        |
| `PLUGIN_OPTIONS`                  | Additional options to pass to go-convert CLI  |               |
| `PLUGIN_LOG_LEVEL`                | Log level (`debug`, `trace`)                  |               |

#### CLI Options

The `PLUGIN_OPTIONS` variable allows you to pass any additional flags directly to the go-convert CLI. For example:

```
PLUGIN_OPTIONS="--org 'My Organization' --project DevOps --pipeline 'Daily Build' --docker-connector docker-hub"
```

Available CLI options include:

| Option               | Description                          | Default    |
|---------------------|--------------------------------------|------------|
| `--org`             | Harness organization                 | `default`  |
| `--project`         | Harness project                      | `default`  |
| `--pipeline`        | Harness pipeline name                | `default`  |
| `--repo-connector`  | Repository connector                 |            |
| `--kube-connector`  | Kubernetes connector                 |            |
| `--kube-namespace`  | Kubernetes namespace                 |            |
| `--docker-connector`| Docker connector                     |            |
| `--org-secrets`     | Organization secrets (comma-separated)|            |

#### Plugin-specific options (via `PLUGIN_OPTIONS`)

These options are interpreted by this plugin (not passed to `go-convert`):

- `--repo-name <value>` or `--repo-name=<value>`
  - Injects `pipeline.properties.ci.codebase.repoName = <value>` into the converted YAML after `go-convert` runs.
  - Quoted values are supported (e.g., `--repo-name "My Repo"`).

Notes:
- `PLUGIN_OPTIONS` parsing now honors shell-like quoting (via shlex), so arguments with spaces remain intact.
- All other options continue to be passed through to `go-convert` unchanged.

### Outputs

| Environment Variable          | Description                                    |
|-------------------------------|------------------------------------------------|
| `PLUGIN_HARNESS_YAML_PATH`    | Path to the generated Harness YAML file         |
| `PLUGIN_HARNESS_YAML`         | Base64-encoded content of the generated Harness YAML |

#### Decoding `PLUGIN_HARNESS_YAML`

- Bash:
  ```bash
  echo "$PLUGIN_HARNESS_YAML" | base64 -d > harness_pipeline.yaml
  ```
- Go:
  ```go
  decoded, err := base64.StdEncoding.DecodeString(os.Getenv("PLUGIN_HARNESS_YAML"))
  ```
- Java:
  ```java
  byte[] decoded = java.util.Base64.getDecoder().decode(System.getenv("PLUGIN_HARNESS_YAML"));
  ```

### Example usage with `--repo-name`

```yaml
steps:
  - name: convert-to-harness
    image: harnesscommunity/drone-convert-to-harness
    settings:
      source_yaml: .drone.yml
      options: --repo-connector opdrones3 --repo-name heehah --docker-connector harnesscommunity --org-secrets docker_password --build-branch master
```

## Implementation Details

This plugin is a wrapper around the go-convert tool from the Harness team. It:

1. Takes input parameters as environment variables
2. Executes the go-convert binary with appropriate arguments
3. Saves the converted YAML to the specified output path
4. Sets output environment variables for use in subsequent pipeline steps

The plugin automatically builds the go-convert binary from the CI-17808 branch during the Docker image build process.

## Local Testing

For local testing, you can use the provided run-test.sh script:

```bash
# Clone the repository
git clone https://github.com/harness-community/drone-convert-to-harness.git
cd drone-convert-to-harness

# Run the test script
./run-test.sh
```

## License

This project is licensed under the terms of the Blue Oak Model License 1.0.0.