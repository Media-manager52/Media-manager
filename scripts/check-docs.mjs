// Validates relative Markdown links and heading anchors across the repository.
//
// Usage: npm run docs:check   (or: node scripts/check-docs.mjs)
//
// Exits non-zero and prints every broken link. External links (http/https/
// mailto) are not fetched; only local targets and anchors are checked.

import { readFile, readdir, stat } from "node:fs/promises"
import { dirname, join, relative, resolve } from "node:path"
import process from "node:process"

const root = resolve(new URL("..", import.meta.url).pathname)
const excludedDirectories = new Set([
  ".git",
  ".next",
  ".pi",
  ".sites-runtime",
  ".wrangler",
  "data",
  "dist",
  "drizzle",
  "node_modules",
])

async function collectMarkdown(directory, results = []) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (excludedDirectories.has(entry.name)) continue
      await collectMarkdown(join(directory, entry.name), results)
    } else if (entry.isFile() && entry.name.endsWith(".md")) {
      results.push(join(directory, entry.name))
    }
  }
  return results
}

function slug(text) {
  // Mirrors GitHub's heading anchors: lowercase, punctuation removed, and each
  // whitespace character replaced by one hyphen (so "a  b" becomes "a--b").
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\w\s-]/g, "")
    .replace(/\s/g, "-")
}

async function headingsOf(path) {
  const body = await readFile(path, "utf8")
  const headings = new Set()
  for (const line of body.split("\n")) {
    const match = /^#{1,6}\s+(.*)$/.exec(line)
    if (match) headings.add(slug(match[1]))
  }
  return headings
}

const linkPattern = /\]\(([^)]+)\)/g
const errors = []
const files = await collectMarkdown(root)
const headingCache = new Map()

async function cachedHeadings(path) {
  if (!headingCache.has(path)) headingCache.set(path, await headingsOf(path))
  return headingCache.get(path)
}

for (const file of files) {
  const body = await readFile(file, "utf8")
  const fileDirectory = dirname(file)
  for (const match of body.matchAll(linkPattern)) {
    const target = match[1].trim()
    if (target.startsWith("http://") || target.startsWith("https://") || target.startsWith("mailto:")) {
      continue
    }
    const hashIndex = target.indexOf("#")
    const pathPart = hashIndex === -1 ? target : target.slice(0, hashIndex)
    const anchor = hashIndex === -1 ? "" : target.slice(hashIndex + 1)
    const display = `${relative(root, file)} -> ${target}`

    if (pathPart === "") {
      if (anchor && !(await cachedHeadings(file)).has(anchor)) {
        errors.push(`${display} (missing anchor in the same file)`)
      }
      continue
    }

    const resolved = resolve(fileDirectory, pathPart)
    let targetStat
    try {
      targetStat = await stat(resolved)
    } catch {
      errors.push(`${display} (target does not exist)`)
      continue
    }
    if (targetStat.isDirectory()) {
      // Directory links are valid (for example ../architecture/); anchors do
      // not apply to directories.
      continue
    }
    if (!targetStat.isFile()) {
      errors.push(`${display} (target is neither file nor directory)`)
      continue
    }
    if (anchor && resolved.endsWith(".md") && !(await cachedHeadings(resolved)).has(anchor)) {
      errors.push(`${display} (missing anchor)`)
    }
  }
}

if (errors.length > 0) {
  console.error(`docs:check failed with ${errors.length} broken link(s):`)
  for (const error of errors.sort()) console.error(`  - ${error}`)
  process.exit(1)
}

console.log(`docs:check passed: ${files.length} Markdown files, all relative links and anchors valid.`)
