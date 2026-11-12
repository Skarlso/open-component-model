---
title: ocm bootstrap
description: Run the bootstrap resource of a component version (EXPERIMENTAL).
suppressTitle: true
toc: true
sidebar:
  collapsed: true
---

## ocm bootstrap

Run the bootstrap resource of a component version (EXPERIMENTAL)

### Synopsis

Run the resource labeled "ocm.software/bootstrap: true" of a component version.

This command is a proof of concept. Its flags, environment and behaviour may change at any time.

The bootstrap resource is run as a container if its type is "ociImage" or "ociArtifact",
and as a local executable otherwise. Executables may carry "os" and "architecture" extra
identities; only the one matching the current platform is run.

The bootstrapper is told what to work on through the environment:

  OCM_BOOTSTRAP_COMPONENT  name of the component version being bootstrapped
  OCM_BOOTSTRAP_VERSION    its version
  OCM_BOOTSTRAP_VALUES     path to the file given with --values, if any
  OCM_BOOTSTRAP_OCM        path to the ocm binary running the bootstrap (executables only)
  OCM_CONFIG               the effective ocm configuration, with a resolver for the component

Because of the resolver, the bootstrapper can address its own component by name alone,
for example "ocm download resource $OCM_BOOTSTRAP_COMPONENT:$OCM_BOOTSTRAP_VERSION --identity name=config",
no matter which repository it was transferred to. For executables, "ocm" on PATH is the
binary running the bootstrap.

Arguments after "--" are passed to the bootstrapper.

```
ocm bootstrap {reference} [-- args...] [flags]
```

### Examples

```
  # Run the bootstrapper of a component version
  ocm bootstrap ghcr.io/acme//acme.org/platform:1.0.0

  # Hand user configuration to the bootstrapper and pass it arguments
  ocm bootstrap ./ctf//acme.org/platform:1.0.0 --values ./values.yaml -- install
```

### Options

```
  -h, --help            help for bootstrap
      --values string   file handed to the bootstrapper as OCM_BOOTSTRAP_VALUES. OCM does not read or validate it.
```

### Options inherited from parent commands

```
      --config stringArray                 supply configuration by a given configuration file.
                                           By default (without specifying custom locations with this flag), the file will be read from one of the well known locations:
                                           1. The path specified in the OCM_CONFIG environment variable
                                           2. The XDG_CONFIG_HOME directory (if set), or the default XDG home ($HOME/.config), or the user's home directory
                                           - $XDG_CONFIG_HOME/ocm/config
                                           - $XDG_CONFIG_HOME/.ocmconfig
                                           - $HOME/.config/ocm/config
                                           - $HOME/.config/.ocmconfig
                                           - $HOME/.ocm/config
                                           - $HOME/.ocmconfig
                                           3. The current working directory:
                                           - $PWD/.ocm/config
                                           - $PWD/.ocmconfig
                                           4. The directory of the current executable:
                                           - $EXE_DIR/.ocm/config
                                           - $EXE_DIR/.ocmconfig
                                           If multiple configuration files are found, they will be merged in the order they are discovered.
                                           Later entries have higher priority.
                                           Using the option, the specified configuration file(s) will be used instead of the lookup above.
                                           Use "-" to read configuration documents from stdin. It is an entry like any other: on its own it
                                           replaces the lookup above, so add --config <file> to keep a file, and a repeated "-" is ignored.
                                           Other documents on stdin stay there for commands that read it, such as transfer with --transfer-spec -.
      --logformat enum                     set the log output format that is used to print individual logs
                                              json: Output logs in JSON format, suitable for machine processing
                                              text: Output logs in human-readable text format, suitable for console output
                                              setting this flag disables the interactive progress display so logs are printed in the chosen format
                                           (must be one of [json text]) (default text)
      --loglevel enum                      sets the logging level
                                              debug: Show all logs including detailed debugging information
                                              info:  Show informational messages and above
                                              warn:  Show warnings and errors only (default)
                                              error: Show errors only
                                           (must be one of [debug error info warn]) (default info)
      --logoutput enum                     set the log output destination
                                              stdout: Write logs to standard output
                                              stderr: Write logs to standard error, useful for separating logs from normal output
                                           (must be one of [stderr stdout]) (default stderr)
      --plugin-directory string            default directory path for ocm plugins. (default "$HOME/.config/ocm/plugins")
      --plugin-shutdown-timeout duration   Timeout for plugin shutdown. If a plugin does not shut down within this time, it is forcefully killed (default 10s)
      --temp-folder string                 Specify a custom temporary folder path for filesystem operations.
      --working-directory string           Specify a custom working directory path to load resources from.
```

### SEE ALSO

* [ocm]({{< relref "ocm.md" >}})	 - The official Open Component Model (OCM) CLI

