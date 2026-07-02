#!@node@
import fs from "node:fs";
import path from "node:path";
import {
  Y as ConfigResolver,
  s as toolRuleFactories,
  B as toolCommandFactories,
  _ as toolSkillFactories,
  f as toolSubagentFactories,
  O as toolMcpFactories,
  I as toolHooksFactories,
  M as toolIgnoreFactories,
  w as toolPermissionsFactories,
  a as RulesProcessor,
  R as CommandsProcessor,
  h as SkillsProcessor,
  u as SubagentsProcessor,
  E as McpProcessor,
  P as HooksProcessor,
  A as IgnoreProcessor,
  S as PermissionsProcessor,
} from "@rulesyncDistImport@";

const policy = JSON.parse(fs.readFileSync("@rulesyncPolicy@", "utf8"));
const neverWritableFiles = new Set(policy.neverWritableFiles ?? []);
const neverWritableDirs = new Set(policy.neverWritableDirs ?? []);
const dynamicFileAlternatives = new Map(Object.entries(policy.dynamicFileAlternatives ?? {}));

const logger = {
  debug() {},
  info() {},
  success() {},
  warn(message) {
    console.error("rulesync-jail-scope: " + String(message));
  },
  error(message) {
    console.error("rulesync-jail-scope: " + String(message));
  },
};

function die(message) {
  throw new Error(message);
}

function parseCommaSeparatedList(value, optionName) {
  if (value === undefined) return undefined;
  if (value === "") die(optionName + " cannot be empty");
  return value
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
}

function takeValue(args, index, optionName) {
  if (index + 1 >= args.length) die("missing value for " + optionName);
  return [args[index + 1], index + 1];
}

function splitCommand(argv) {
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--") break;
    if (arg === "--json" || arg === "-j") continue;
    if (arg.startsWith("-")) continue;
    return { command: arg, args: argv.slice(i + 1) };
  }
  return { command: "", args: [] };
}

function rejectForbiddenOptions(args) {
  for (const arg of args) {
    if (arg === "--") break;
    if (
      arg === "--global" ||
      arg.startsWith("--global=") ||
      arg === "-g" ||
      arg === "--input-root" ||
      arg.startsWith("--input-root=")
    ) {
      die("option is intentionally disabled in this jail: " + arg);
    }
  }
}

function parseCommonOptions(args, allowed) {
  const options = {};

  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    let value;

    if (arg === "--") break;

    if (arg === "--targets" || arg === "-t") {
      if (!allowed.targets) continue;
      [value, i] = takeValue(args, i, arg);
      options.targets = parseCommaSeparatedList(value, arg);
      continue;
    }
    if (arg.startsWith("--targets=")) {
      if (allowed.targets) {
        options.targets = parseCommaSeparatedList(arg.slice("--targets=".length), "--targets");
      }
      continue;
    }
    if (arg.startsWith("-t") && arg.length > 2) {
      if (allowed.targets) options.targets = parseCommaSeparatedList(arg.slice(2), "-t");
      continue;
    }

    if (arg === "--features" || arg === "-f") {
      if (!allowed.features) continue;
      [value, i] = takeValue(args, i, arg);
      options.features = parseCommaSeparatedList(value, arg);
      continue;
    }
    if (arg.startsWith("--features=")) {
      if (allowed.features) {
        options.features = parseCommaSeparatedList(arg.slice("--features=".length), "--features");
      }
      continue;
    }
    if (arg.startsWith("-f") && arg.length > 2) {
      if (allowed.features) options.features = parseCommaSeparatedList(arg.slice(2), "-f");
      continue;
    }

    if (arg === "--output-roots" || arg === "-o") {
      if (!allowed.outputRoots) continue;
      [value, i] = takeValue(args, i, arg);
      options.outputRoots = parseCommaSeparatedList(value, arg);
      continue;
    }
    if (arg.startsWith("--output-roots=")) {
      if (allowed.outputRoots) {
        options.outputRoots = parseCommaSeparatedList(
          arg.slice("--output-roots=".length),
          "--output-roots",
        );
      }
      continue;
    }
    if (arg.startsWith("-o") && arg.length > 2) {
      if (allowed.outputRoots) options.outputRoots = parseCommaSeparatedList(arg.slice(2), "-o");
      continue;
    }

    if (arg === "--config" || arg === "-c") {
      if (!allowed.configPath) continue;
      [value, i] = takeValue(args, i, arg);
      options.configPath = value;
      continue;
    }
    if (arg.startsWith("--config=")) {
      if (allowed.configPath) options.configPath = arg.slice("--config=".length);
      continue;
    }
    if (arg.startsWith("-c") && arg.length > 2) {
      if (allowed.configPath) options.configPath = arg.slice(2);
      continue;
    }

    if (arg === "--delete" && allowed.delete) {
      options.delete = true;
      continue;
    }
    if (arg === "--dry-run" && allowed.dryRun) {
      options.dryRun = true;
      continue;
    }
    if (arg === "--check" && allowed.check) {
      options.check = true;
      continue;
    }
    if (arg === "--simulate-commands" && allowed.simulateCommands) {
      options.simulateCommands = true;
      continue;
    }
    if (arg === "--simulate-subagents" && allowed.simulateSubagents) {
      options.simulateSubagents = true;
      continue;
    }
    if (arg === "--simulate-skills" && allowed.simulateSkills) {
      options.simulateSkills = true;
      continue;
    }
    if (arg === "--verbose" || arg === "-V") {
      options.verbose = true;
      continue;
    }
    if (arg === "--silent" || arg === "-s") {
      options.silent = true;
      continue;
    }
  }

  return options;
}

function parseConvertOptions(args) {
  const options = parseCommonOptions(args, {
    features: true,
    dryRun: true,
  });

  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    let value;
    if (arg === "--") break;

    if (arg === "--from") {
      [value, i] = takeValue(args, i, arg);
      options.from = value;
      continue;
    }
    if (arg.startsWith("--from=")) {
      options.from = arg.slice("--from=".length);
      continue;
    }
    if (arg === "--to") {
      [value, i] = takeValue(args, i, arg);
      options.to = parseCommaSeparatedList(value, arg);
      continue;
    }
    if (arg.startsWith("--to=")) {
      options.to = parseCommaSeparatedList(arg.slice("--to=".length), "--to");
      continue;
    }
  }

  if (!options.from) die("convert requires --from");
  if (!options.to || options.to.length === 0) die("convert requires --to");
  return options;
}

function toPosix(value) {
  return value.replaceAll(path.sep, "/").replaceAll("\\", "/");
}

function validateRelativePath(relativePath, label) {
  if (typeof relativePath !== "string") die(label + " must be a string");

  const posix = toPosix(relativePath);
  if (posix.trim() === "") die(label + " cannot be empty");
  if (path.posix.isAbsolute(posix)) die(label + " must not be absolute: " + posix);

  const parts = posix.split("/");
  if (parts.includes("..")) die(label + " must not contain '..': " + posix);

  const normalized = path.posix.normalize(posix);
  if (normalized === ".") return ".";
  if (normalized.startsWith("../") || normalized === "..") {
    die(label + " escapes project root: " + posix);
  }

  return normalized;
}

function joinRelative(...parts) {
  const filtered = parts.filter(
    (part) => part !== undefined && part !== null && part !== "" && part !== ".",
  );
  if (filtered.length === 0) return ".";
  return validateRelativePath(
    path.posix.join(...filtered.map((part) => toPosix(String(part)))),
    "relative output path",
  );
}

function projectRelative(projectRoot, absolutePath, label) {
  const resolved = path.resolve(absolutePath);
  const relativePath = path.relative(projectRoot, resolved);

  if (relativePath === "") return ".";
  if (relativePath.startsWith("..") || path.isAbsolute(relativePath)) {
    die(label + " outside project root: " + resolved);
  }

  return toPosix(relativePath);
}

function withOutputRoot(outputRootRelative, relativePath) {
  if (outputRootRelative === ".") return relativePath;
  if (relativePath === ".") return outputRootRelative;
  return joinRelative(outputRootRelative, relativePath);
}

function parentDirs(relativePath) {
  if (relativePath === ".") return [];

  const parts = relativePath.split("/");
  const dirs = [];

  for (let i = 1; i < parts.length; i += 1) {
    dirs.push(parts.slice(0, i).join("/"));
  }

  return dirs;
}

function classifyFile(relativePath) {
  const ext = path.posix.extname(relativePath).toLowerCase();
  if (ext === ".json" || ext === ".jsonc") return "jsonFiles";
  if (ext === ".toml") return "tomlFiles";
  if (ext === ".yaml" || ext === ".yml") return "yamlFiles";
  return "emptyFiles";
}

function createScope(command, preview = false) {
  return {
    command,
    preview,
    emptyFiles: new Set(),
    jsonFiles: new Set(),
    tomlFiles: new Set(),
    yamlFiles: new Set(),
    dirs: new Set(),
    readFiles: new Set(),
    readDirs: new Set(),
    mountDirs: new Set(),
  };
}

function addMountParents(scope, relativePath) {
  for (const dir of parentDirs(relativePath)) scope.mountDirs.add(dir);
}

function checkNeverWritable(relativePath, label) {
  if (neverWritableFiles.has(relativePath) || neverWritableDirs.has(relativePath)) {
    die("refusing never-writable " + label + ": " + relativePath);
  }
  for (const deniedDir of neverWritableDirs) {
    if (relativePath.startsWith(deniedDir + "/")) {
      die("refusing never-writable " + label + ": " + relativePath);
    }
  }
}

function addDir(scope, relativeDirPath) {
  const normalized = validateRelativePath(relativeDirPath, "writable directory");
  if (normalized === ".") return;
  checkNeverWritable(normalized, "output directory");

  scope.dirs.add(normalized);
  addMountParents(scope, normalized);
}

function addFile(scope, relativeFilePath) {
  const normalized = validateRelativePath(relativeFilePath, "writable file");
  if (normalized === ".") die("writable file cannot be project root");
  checkNeverWritable(normalized, "output file");

  scope[classifyFile(normalized)].add(normalized);
  addMountParents(scope, normalized);
}

function addReadDir(scope, relativeDirPath) {
  const normalized = validateRelativePath(relativeDirPath, "read directory");
  if (normalized === ".") return;

  scope.readDirs.add(normalized);
  addMountParents(scope, normalized);
}

function addReadFile(scope, relativeFilePath) {
  const normalized = validateRelativePath(relativeFilePath, "read file");
  if (normalized === ".") return;

  scope.readFiles.add(normalized);
  addMountParents(scope, normalized);
}

function dynamicAlternatives(relativeDirPath, relativeFilePath) {
  const dir = validateRelativePath(relativeDirPath, "relativeDirPath");
  const file = validateRelativePath(relativeFilePath, "relativeFilePath");
  const key = joinRelative(dir, file);
  const alternatives = dynamicFileAlternatives.get(key) ?? dynamicFileAlternatives.get(file);
  if (!alternatives) return [joinRelative(dir, file)];
  return alternatives.map((alternative) => joinRelative(dir, alternative));
}

function selectWritableFile(projectRoot, outputRootRelative, relativeDirPath, relativeFilePath) {
  const alternatives = dynamicAlternatives(relativeDirPath, relativeFilePath);
  if (alternatives.length === 1) return alternatives[0];

  for (const candidate of alternatives) {
    if (fs.existsSync(path.join(projectRoot, withOutputRoot(outputRootRelative, candidate)))) {
      return candidate;
    }
  }

  return alternatives[0];
}

function addDescriptor(scope, projectRoot, outputRootRelative, descriptor, access) {
  if (!descriptor || typeof descriptor !== "object") return;
  if (typeof descriptor.relativeDirPath !== "string") return;

  const relativeDirPath = validateRelativePath(descriptor.relativeDirPath, "relativeDirPath");

  if (typeof descriptor.relativeFilePath === "string") {
    if (access === "read") {
      for (const file of dynamicAlternatives(relativeDirPath, descriptor.relativeFilePath)) {
        addReadFile(scope, withOutputRoot(outputRootRelative, file));
      }
      return;
    }

    const selected = selectWritableFile(
      projectRoot,
      outputRootRelative,
      relativeDirPath,
      descriptor.relativeFilePath,
    );
    addFile(scope, withOutputRoot(outputRootRelative, selected));
  } else if (access === "read") {
    addReadDir(scope, withOutputRoot(outputRootRelative, relativeDirPath));
  } else {
    addDir(scope, withOutputRoot(outputRootRelative, relativeDirPath));
  }
}

function addSettablePaths(scope, projectRoot, outputRootRelative, paths, access) {
  if (!paths || typeof paths !== "object") return;

  addDescriptor(scope, projectRoot, outputRootRelative, paths, access);
  addDescriptor(scope, projectRoot, outputRootRelative, paths.root, access);
  addDescriptor(scope, projectRoot, outputRootRelative, paths.nonRoot, access);
  addDescriptor(scope, projectRoot, outputRootRelative, paths.recommended, access);
  addDescriptor(scope, projectRoot, outputRootRelative, paths.legacy, access);

  if (Array.isArray(paths.alternativeSkillRoots)) {
    for (const relativeDirPath of paths.alternativeSkillRoots) {
      const normalized = validateRelativePath(relativeDirPath, "alternative skill root");
      if (access === "read") {
        addReadDir(scope, withOutputRoot(outputRootRelative, normalized));
      } else {
        addDir(scope, withOutputRoot(outputRootRelative, normalized));
      }
    }
  }
}

function addPolicyWrites(scope, feature) {
  const writes = policy.rulesyncWrites?.[feature];
  if (!writes) return;

  for (const relativePath of writes.emptyFiles ?? []) addFile(scope, relativePath);
  for (const relativePath of writes.jsonFiles ?? []) addFile(scope, relativePath);
  for (const relativePath of writes.tomlFiles ?? []) addFile(scope, relativePath);
  for (const relativePath of writes.yamlFiles ?? []) addFile(scope, relativePath);
  for (const relativePath of writes.dirs ?? []) addDir(scope, relativePath);
}

function getFeatureDescriptors(config, mode) {
  const global = false;
  const simulated = mode === "generate";
  const importOnly = mode === "importSource" || mode === "convertSource";

  return {
    rules: {
      factories: toolRuleFactories,
      supportedTargets: RulesProcessor.getToolTargets({ global }),
    },
    ignore: {
      factories: toolIgnoreFactories,
      supportedTargets: IgnoreProcessor.getToolTargets({ global }),
    },
    mcp: {
      factories: toolMcpFactories,
      supportedTargets: McpProcessor.getToolTargets({ global }),
    },
    commands: {
      factories: toolCommandFactories,
      supportedTargets: CommandsProcessor.getToolTargets({
        global,
        includeSimulated: simulated && config.getSimulateCommands(),
      }),
    },
    subagents: {
      factories: toolSubagentFactories,
      supportedTargets: SubagentsProcessor.getToolTargets({
        global,
        includeSimulated: simulated && config.getSimulateSubagents(),
      }),
    },
    skills: {
      factories: toolSkillFactories,
      supportedTargets: SkillsProcessor.getToolTargets({
        global,
        includeSimulated: simulated && config.getSimulateSkills(),
      }),
    },
    hooks: {
      factories: toolHooksFactories,
      supportedTargets: HooksProcessor.getToolTargets({ global, importOnly }),
    },
    permissions: {
      factories: toolPermissionsFactories,
      supportedTargets: PermissionsProcessor.getToolTargets({ global, importOnly }),
    },
  };
}

function addToolScope({
  scope,
  projectRoot,
  outputRootRelative,
  config,
  target,
  feature,
  access,
  mode,
}) {
  const descriptors = getFeatureDescriptors(config, mode);
  const descriptor = descriptors[feature];
  if (!descriptor) return;
  if (!descriptor.supportedTargets.includes(target)) return;

  const factory = descriptor.factories.get(target);
  if (!factory) return;

  const settablePaths = factory.class.getSettablePaths({
    global: false,
    options: config.getFeatureOptions(target, feature),
    excludeToolDir: false,
  });

  addSettablePaths(scope, projectRoot, outputRootRelative, settablePaths, access);
}

function sorted(set) {
  return [...set].sort((a, b) => a.localeCompare(b));
}

function scopeResult(scope) {
  return {
    command: scope.command,
    preview: scope.preview,
    emptyFiles: sorted(scope.emptyFiles),
    jsonFiles: sorted(scope.jsonFiles),
    tomlFiles: sorted(scope.tomlFiles),
    yamlFiles: sorted(scope.yamlFiles),
    dirs: sorted(scope.dirs),
    readFiles: sorted(scope.readFiles),
    readDirs: sorted(scope.readDirs),
    mountDirs: sorted(scope.mountDirs),
  };
}

function validateConfig(config, projectRoot) {
  if (config.getGlobal()) die("global output is intentionally disabled in this jail");

  const inputRoot = path.resolve(config.getInputRoot());
  if (inputRoot !== projectRoot) {
    die("input root outside project root is intentionally disabled: " + inputRoot);
  }

  return config.getOutputRoots().map((outputRoot) =>
    projectRelative(projectRoot, outputRoot, "output root"),
  );
}

async function buildGenerateScope(projectRoot, args) {
  const options = parseCommonOptions(args, {
    targets: true,
    features: true,
    outputRoots: true,
    configPath: true,
    delete: true,
    dryRun: true,
    check: true,
    simulateCommands: true,
    simulateSubagents: true,
    simulateSkills: true,
  });
  const config = await ConfigResolver.resolve(options, { logger });
  const outputRoots = validateConfig(config, projectRoot);
  const scope = createScope("generate", config.isPreviewMode());

  for (const outputRootRelative of outputRoots) {
    for (const target of config.getTargets()) {
      for (const feature of config.getFeatures(target)) {
        addToolScope({
          scope,
          projectRoot,
          outputRootRelative,
          config,
          target,
          feature,
          access: "write",
          mode: "generate",
        });
      }
    }
  }

  return scope;
}

async function buildGitignoreScope(projectRoot, args) {
  parseCommonOptions(args, {
    targets: true,
    features: true,
  });
  const config = await ConfigResolver.resolve({}, { logger });
  validateConfig(config, projectRoot);

  const scope = createScope("gitignore", false);
  for (const relativePath of policy.vcsManagedFiles ?? []) addFile(scope, relativePath);
  return scope;
}

async function buildImportScope(projectRoot, args) {
  const options = parseCommonOptions(args, {
    targets: true,
    features: true,
  });
  if (!options.targets) die("import requires --targets");
  if (options.targets.length !== 1) die("import requires exactly one --targets entry");

  const config = await ConfigResolver.resolve(options, { logger });
  const outputRootRelative = validateConfig(config, projectRoot)[0] ?? ".";
  const target = config.getTargets()[0];
  if (!target) die("import target did not resolve");

  const scope = createScope("import", false);
  for (const feature of config.getFeatures(target)) {
    addToolScope({
      scope,
      projectRoot,
      outputRootRelative,
      config,
      target,
      feature,
      access: "read",
      mode: "importSource",
    });
    addPolicyWrites(scope, feature);
  }

  return scope;
}

async function buildConvertScope(projectRoot, args) {
  const options = parseConvertOptions(args);
  const toTools = [...new Set(options.to)];
  const config = await ConfigResolver.resolve(
    {
      targets: [options.from, ...toTools],
      features: options.features ?? ["*"],
      dryRun: options.dryRun,
      verbose: options.verbose,
      silent: options.silent,
    },
    { logger },
  );
  const outputRootRelative = validateConfig(config, projectRoot)[0] ?? ".";

  const scope = createScope("convert", config.isPreviewMode());
  for (const feature of config.getFeatures(options.from)) {
    addToolScope({
      scope,
      projectRoot,
      outputRootRelative,
      config,
      target: options.from,
      feature,
      access: "read",
      mode: "convertSource",
    });

    for (const target of toTools) {
      addToolScope({
        scope,
        projectRoot,
        outputRootRelative,
        config,
        target,
        feature,
        access: "write",
        mode: "convertDest",
      });
    }
  }

  return scope;
}

async function main() {
  const projectRoot = path.resolve(process.cwd());
  const argv = process.argv.slice(2);
  const { command, args } = splitCommand(argv);

  rejectForbiddenOptions(argv);

  let scope;
  switch (command) {
    case "generate":
      scope = await buildGenerateScope(projectRoot, args);
      break;
    case "gitignore":
      scope = await buildGitignoreScope(projectRoot, args);
      break;
    case "import":
      scope = await buildImportScope(projectRoot, args);
      break;
    case "convert":
      scope = await buildConvertScope(projectRoot, args);
      break;
    default:
      die("scope helper does not support command: " + command);
  }

  process.stdout.write(JSON.stringify(scopeResult(scope)) + "\n");
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error("rulesync-jail-scope: " + message);
  process.exit(2);
});
