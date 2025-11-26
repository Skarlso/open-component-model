import assert from "assert";
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import yaml from "js-yaml";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Import the functions we want to test (need to export them first)
// For now, test the integration by checking the structure

/**
 * Mock core object for testing
 */
class MockCore {
  constructor() {
    this.outputs = {};
    this.infos = [];
    this.warnings = [];
    this.errors = [];
    this.summaryData = null;
  }

  setOutput(name, value) {
    this.outputs[name] = value;
  }

  info(message) {
    this.infos.push(message);
  }

  warning(message) {
    this.warnings.push(message);
  }

  setFailed(message) {
    this.errors.push(message);
    throw new Error(message);
  }

  get summary() {
    const self = this;
    return {
      data: {},
      addHeading(text) {
        this.data.heading = text;
        return this;
      },
      addRaw(text) {
        this.data.raw = text;
        return this;
      },
      addTable(table) {
        this.data.table = table;
        return this;
      },
      async write() {
        self.summaryData = this.data;
      },
    };
  }
}

// ----------------------------------------------------------
// Test constructor template loading
// ----------------------------------------------------------
const constructorPath = path.join(
  __dirname,
  "../config/plugin-registry-constructor.yaml"
);
assert.ok(
  fs.existsSync(constructorPath),
  "Constructor template should exist"
);

const template = yaml.load(fs.readFileSync(constructorPath, "utf8"));
assert.strictEqual(
  template.name,
  "ocm.software/plugin-registry",
  "Template should have correct component name"
);
assert.strictEqual(
  template.version,
  "((REGISTRY_VERSION))",
  "Template should have version placeholder"
);
assert.ok(
  Array.isArray(template.componentReferences),
  "Template should have componentReferences array"
);

console.log("✓ Constructor template structure is valid");

// ----------------------------------------------------------
// Test environment variable validation
// ----------------------------------------------------------
const originalEnv = { ...process.env };

async function testMissingEnvVars() {
  // Clear relevant env vars
  delete process.env.REPOSITORY_OWNER;
  delete process.env.PLUGIN_NAME;
  delete process.env.PLUGIN_COMPONENT;
  delete process.env.PLUGIN_VERSION;
  delete process.env.CONSTRUCTOR_PATH;

  const core = new MockCore();

  try {
    const script = await import("./update-plugin-registry.js");
    await script.default({ core });
    assert.fail("Should have failed with missing env vars");
  } catch (error) {
    assert.ok(
      error.message.includes("Missing required environment variables"),
      "Should fail with missing env vars error"
    );
  }

  // Restore env
  Object.assign(process.env, originalEnv);
}

await testMissingEnvVars();
console.log("✓ Environment variable validation works");

// ----------------------------------------------------------
// All tests passed
// ----------------------------------------------------------
console.log("\nAll tests passed!");
