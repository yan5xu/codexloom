import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const outDir = path.resolve(webDir, "../internal/webui/dist");
const assetsDir = path.join(outDir, "assets");
const manifestPath = path.join(outDir, ".vite/manifest.json");
const previousAssetsPath = path.join(outDir, ".vite/previous-assets.json");

function outputFiles(manifest) {
  const files = new Set();
  for (const entry of Object.values(manifest)) {
    if (entry.file) files.add(entry.file);
    for (const file of entry.css || []) files.add(file);
    for (const file of entry.assets || []) files.add(file);
  }
  return files;
}

function assetFiles(dir, files = []) {
  if (!existsSync(dir)) return files;
  for (const name of readdirSync(dir)) {
    const file = path.join(dir, name);
    if (statSync(file).isDirectory()) assetFiles(file, files);
    else files.push(path.relative(outDir, file));
  }
  return files;
}

const filesBeforeBuild = new Set(assetFiles(assetsDir));
const oldCurrentFiles = existsSync(manifestPath)
  ? outputFiles(JSON.parse(readFileSync(manifestPath, "utf8")))
  : new Set();
let previousFiles = existsSync(previousAssetsPath)
  ? new Set(JSON.parse(readFileSync(previousAssetsPath, "utf8")))
  : new Set([...filesBeforeBuild].filter((file) => !oldCurrentFiles.has(file)));
if (oldCurrentFiles.size === 0) previousFiles = filesBeforeBuild;

execFileSync(path.join(webDir, "node_modules/.bin/vite"), ["build"], {
  cwd: webDir,
  stdio: "inherit",
});

const currentFiles = outputFiles(JSON.parse(readFileSync(manifestPath, "utf8")));
const currentChanged = oldCurrentFiles.size > 0
  && (oldCurrentFiles.size !== currentFiles.size || [...oldCurrentFiles].some((file) => !currentFiles.has(file)));
if (currentChanged) previousFiles = oldCurrentFiles;

for (const file of assetFiles(assetsDir)) {
  if (!previousFiles.has(file) && !currentFiles.has(file)) {
    rmSync(path.join(outDir, file));
  }
}

writeFileSync(previousAssetsPath, `${JSON.stringify([...previousFiles].sort(), null, 2)}\n`);
