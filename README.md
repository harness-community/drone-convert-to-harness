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

| Environment Variable              | Description                                  | Default Value                                    |
| --------------------------------- | -------------------------------------------- | ------------------------------------------------ |
| `PLUGIN_CI_PROVIDER`              | CI provider type                             | `drone`                                          |
| `PLUGIN_SOURCE_YAML`              | Path to source YAML file                     |                                                  |
| `PLUGIN_HARNESS_OUTPUT_YAML_PATH` | Output path for converted YAML               | `$DRONE_WORKSPACE/convert/harness_pipeline.yaml` |
| `PLUGIN_DOWNGRADE`                | Whether to downgrade the YAML                | `true`                                           |
| `PLUGIN_OPTIONS`                  | Additional options to pass to go-convert CLI |                                                  |
| `PLUGIN_LOG_LEVEL`                | Log level (`debug`, `trace`)                 |                                                  |

#### CLI Options

The `PLUGIN_OPTIONS` variable allows you to pass any additional flags directly to the go-convert CLI. For example:

```
PLUGIN_OPTIONS="--org 'My Organization' --project DevOps --pipeline 'Daily Build' --docker-connector docker-hub"
```

Available CLI options include:

| Option               | Description                            | Default   |
| -------------------- | -------------------------------------- | --------- |
| `--org`              | Harness organization                   | `default` |
| `--project`          | Harness project                        | `default` |
| `--pipeline`         | Harness pipeline name                  | `default` |
| `--repo-connector`   | Repository connector                   |           |
| `--kube-connector`   | Kubernetes connector                   |           |
| `--kube-namespace`   | Kubernetes namespace                   |           |
| `--docker-connector` | Docker connector                       |           |
| `--org-secrets`      | Organization secrets (comma-separated) |           |

#### Plugin-only options (post-processing)

These flags are parsed by the plugin and removed from the arguments passed to `go-convert`. They control the `pipeline.properties.ci.codebase.build` structure in the generated YAML.

| Option             | Effect on generated YAML                       |
| ------------------ | ---------------------------------------------- |
| `--build-branch v` | `build: { type: branch, spec: { branch: v } }` |
| `--build-tag v`    | `build: { type: tag,    spec: { tag: v } }`    |
| `--build-pr v`     | `build: { type: PR,     spec: { number: v } }` |

Notes:

- If multiple `--build-*` flags are provided, the last one wins (a warning is logged).
- Values can be literals (e.g., `123`) or Harness expressions (e.g., `<+trigger.tag>`). Expressions are stored verbatim and resolved at runtime by Harness when applicable.
- If no `--build-*` flag is provided, the plugin preserves whatever `go-convert` outputs (often `<+input>`).

### Outputs

| Environment Variable       | Description                             |
| -------------------------- | --------------------------------------- |
| `PLUGIN_HARNESS_YAML_PATH` | Path to the generated Harness YAML file |

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
