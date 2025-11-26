// @ts-check
import { execSync } from "child_process";
import fs from "fs";
import path from "path";
import yaml from "js-yaml";
import { computeNextVersions } from "./compute-rc-version.js";

/**
 * Configuration for Docker-based OCM CLI operations.
 */
const OCM_CONFIG = {
  cliImage: "ghcr.io/open-component-model/cli:main",
  configPath: "/.ocmconfig",
  dockerConfigPath: "/.docker/config.json",
  certsPath: "/etc/ssl/certs/",
};

/**
 * Execute OCM CLI command in Docker container.
 *
 * @param {Object} options - Command options
 * @param {string[]} options.args - OCM CLI arguments
 * @param {Object.<string, string>} [options.volumes] - Additional volume mounts (hostPath: containerPath)
 * @param {string} [options.workdir] - Working directory inside container
 * @param {boolean} [options.throwOnError] - Whether to throw on non-zero exit (default: true)
 * @returns {string} Command output
 */
function runOcmCommand({ args, volumes = {}, workdir, throwOnError = true }) {
  const homeDir = process.env.HOME || process.env.USERPROFILE;

  // Base volume mounts
  const volumeMounts = [
    `-v "${homeDir}/.docker/config.json:${OCM_CONFIG.dockerConfigPath}:ro"`,
    `-v "${homeDir}/.ocmconfig:${OCM_CONFIG.configPath}:ro"`,
    `-v "${OCM_CONFIG.certsPath}:${OCM_CONFIG.certsPath}:ro"`,
  ];

  // Add custom volumes
  for (const [hostPath, containerPath] of Object.entries(volumes)) {
    volumeMounts.push(`-v "${hostPath}:${containerPath}"`);
  }

  // Build docker command
  const dockerCmd = [
    "docker run --rm",
    ...volumeMounts,
    workdir ? `-w "${workdir}"` : "",
    `"${OCM_CONFIG.cliImage}"`,
    ...args,
  ]
    .filter(Boolean)
    .join(" ");

  try {
    return execSync(dockerCmd, {
      encoding: "utf8",
      stdio: throwOnError ? "pipe" : "inherit",
    });
  } catch (error) {
    if (throwOnError) {
      throw new Error(
        `OCM command failed: ${error.message}\nCommand: ${dockerCmd}`
      );
    }
    return "";
  }
}

/**
 * Fetch current registry version and descriptor.
 *
 * @param {Object} options - Fetch options
 * @param {string} options.repository - OCM repository URL
 * @param {string} options.componentName - Registry component name
 * @returns {Object} Registry info: { exists: boolean, version: string, descriptor: Object|null }
 */
function fetchRegistryVersion({ repository, componentName }) {
  try {
    const output = runOcmCommand({
      args: [
        "get cv",
        `${repository}//${componentName}`,
        "-ojson",
        "--loglevel=error",
        "--latest",
        `--config ${OCM_CONFIG.configPath}`,
      ],
      throwOnError: false,
    });

    if (!output || output.trim() === "") {
      return {
        exists: false,
        version: "v0.0.1",
        descriptor: null,
      };
    }

    const data = JSON.parse(output);
    const component = data[0]?.component;

    if (!component) {
      return {
        exists: false,
        version: "v0.0.1",
        descriptor: null,
      };
    }

    return {
      exists: true,
      version: component.version,
      descriptor: component,
    };
  } catch (error) {
    // If command fails or JSON parse fails, registry doesn't exist
    return {
      exists: false,
      version: "v0.0.1",
      descriptor: null,
    };
  }
}

/**
 * Prepare registry constructor with updated plugin reference.
 *
 * @param {Object} options - Constructor options
 * @param {string} options.constructorPath - Path to constructor template file
 * @param {string} options.registryVersion - Current registry version
 * @param {string} options.pluginName - Plugin name
 * @param {string} options.pluginComponent - Plugin component name
 * @param {string} options.pluginVersion - Plugin version
 * @param {Object|null} options.descriptor - Current registry descriptor (if exists)
 * @returns {Object} Result: { constructor: Object, newVersion: string }
 */
function prepareRegistryConstructor({
  constructorPath,
  registryVersion,
  pluginName,
  pluginComponent,
  pluginVersion,
  descriptor,
}) {
  const template = fs.readFileSync(constructorPath, "utf8");
  const constructor = yaml.load(template);

  if (descriptor) {
    // Registry exists - use existing component references
    constructor.componentReferences = descriptor.componentReferences || [];

    // Check for duplicate plugin+version
    const duplicate = constructor.componentReferences.find(
      (r) => r.name === pluginName && r.version === pluginVersion
    );
    if (duplicate) {
      throw new Error(
        `Plugin ${pluginName}@${pluginVersion} already exists in registry`
      );
    }

    // Determine version bump: minor for new plugin, patch for existing
    const pluginExists = constructor.componentReferences.find(
      (r) => r.name === pluginName
    );
    const bumpMinor = !pluginExists;
    const nextVersion = computeNextVersions(
      registryVersion,
      registryVersion,
      "",
      bumpMinor
    );
    constructor.version = nextVersion.baseVersion;
  } else {
    // New registry
    constructor.componentReferences = [];
    constructor.version = "v0.1.0";
  }

  // Add new plugin reference
  constructor.componentReferences.push({
    name: pluginName,
    componentName: pluginComponent,
    version: pluginVersion,
  });

  return {
    constructor,
    newVersion: constructor.version,
  };
}

/**
 * Publish registry component to repository.
 *
 * @param {Object} options - Publish options
 * @param {string} options.repository - OCM repository URL
 * @param {string} options.constructorPath - Path to constructor YAML file
 */
function publishRegistry({ repository, constructorPath }) {
  const workdir = path.dirname(constructorPath);
  const filename = path.basename(constructorPath);

  runOcmCommand({
    args: [
      "add component-version",
      "--component-version-conflict-policy replace",
      `--config "${OCM_CONFIG.configPath}"`,
      `--repository "${repository}"`,
      `--constructor "./${filename}"`,
    ],
    volumes: {
      [workdir]: workdir,
    },
    workdir,
    throwOnError: true,
  });
}

/**
 * Verify published registry component.
 *
 * @param {Object} options - Verify options
 * @param {string} options.repository - OCM repository URL
 * @param {string} options.componentName - Registry component name
 * @param {string} options.version - Version to verify
 */
function verifyRegistry({ repository, componentName, version }) {
  runOcmCommand({
    args: [
      "get component",
      `--config "${OCM_CONFIG.configPath}"`,
      `"${repository}//${componentName}:${version}"`,
    ],
    throwOnError: true,
  });
}

/**
 * GitHub Actions entrypoint for updating plugin registry.
 *
 * Environment variables:
 * - REPOSITORY_OWNER: Repository owner for GHCR (e.g., ghcr.io/open-component-model)
 * - PLUGIN_NAME: Plugin name (required)
 * - PLUGIN_COMPONENT: Plugin component name (required)
 * - PLUGIN_VERSION: Plugin version (required)
 * - CONSTRUCTOR_PATH: Path to constructor template file (required)
 * - REGISTRY_COMPONENT: Registry component name (default: ocm.software/plugin-registry)
 * - DRY_RUN: If "true", skip publishing (default: false)
 *
 * @param {import('@actions/github-script').AsyncFunctionArguments} args
 */
export default async function updatePluginRegistry({ core }) {
  // Read configuration from environment
  const repository = process.env.REPOSITORY_OWNER;
  const pluginName = process.env.PLUGIN_NAME;
  const pluginComponent = process.env.PLUGIN_COMPONENT;
  const pluginVersion = process.env.PLUGIN_VERSION;
  const constructorPath = process.env.CONSTRUCTOR_PATH;
  const registryComponent =
    process.env.REGISTRY_COMPONENT || "ocm.software/plugin-registry";
  const isDryRun = process.env.DRY_RUN === "true";

  // Validate required inputs
  if (
    !repository ||
    !pluginName ||
    !pluginComponent ||
    !pluginVersion ||
    !constructorPath
  ) {
    core.setFailed(
      "Missing required environment variables: REPOSITORY_OWNER, PLUGIN_NAME, PLUGIN_COMPONENT, PLUGIN_VERSION, CONSTRUCTOR_PATH"
    );
    return;
  }

  try {
    // Step 1: Fetch current registry version
    core.info(`Fetching current registry from ${repository}...`);
    const registry = fetchRegistryVersion({
      repository,
      componentName: registryComponent,
    });

    if (registry.exists) {
      core.info(`Found existing registry at version ${registry.version}`);
    } else {
      core.info("Registry not found, will create new registry");
    }

    // Step 2: Prepare registry constructor
    core.info(`Preparing registry constructor for ${pluginName}@${pluginVersion}...`);
    const { constructor, newVersion } = prepareRegistryConstructor({
      constructorPath,
      registryVersion: registry.version,
      pluginName,
      pluginComponent,
      pluginVersion,
      descriptor: registry.descriptor,
    });

    // Write constructor to file
    const rendered = yaml.dump(constructor, { lineWidth: -1 });
    fs.writeFileSync(constructorPath, rendered, "utf8");
    core.info(`Registry constructor written to ${constructorPath}`);
    core.info(`New registry version: ${newVersion}`);

    // Set outputs for subsequent steps
    core.setOutput("old_version", registry.version);
    core.setOutput("new_version", newVersion);
    core.setOutput("registry_exists", registry.exists);

    if (isDryRun) {
      core.warning("DRY RUN mode - skipping publish and verify steps");

      await core.summary
        .addHeading("Plugin Registry Update (DRY RUN)")
        .addRaw("⚠️ **DRY RUN - No changes published**\n\n")
        .addTable([
          [
            { data: "Field", header: true },
            { data: "Value", header: true },
          ],
          ["Previous Version", registry.version],
          ["New Version", newVersion],
          ["Plugin Name", pluginName],
          ["Plugin Version", pluginVersion],
          ["Plugin Component", pluginComponent],
        ])
        .write();

      return;
    }

    // Step 3: Publish registry component
    core.info(`Publishing registry version ${newVersion}...`);
    publishRegistry({
      repository,
      constructorPath,
    });
    core.info("Registry published successfully");

    // Step 4: Verify published registry
    core.info("Verifying published registry...");
    verifyRegistry({
      repository,
      componentName: registryComponent,
      version: newVersion,
    });
    core.info("Registry verification successful");

    // Generate summary
    await core.summary
      .addHeading("Plugin Registry Update")
      .addTable([
        [
          { data: "Field", header: true },
          { data: "Value", header: true },
        ],
        ["Previous Version", registry.version],
        ["New Version", `✅ ${newVersion}`],
        ["Plugin Name", pluginName],
        ["Plugin Version", pluginVersion],
        ["Plugin Component", pluginComponent],
        [
          "Published Location",
          `\`${repository}//${registryComponent}:${newVersion}\``,
        ],
      ])
      .write();
  } catch (error) {
    core.setFailed(`Failed to update plugin registry: ${error.message}`);
  }
}
