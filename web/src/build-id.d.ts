/**
 * The build identifier, replaced at build time by Vite's `define`.
 *
 * Declared rather than generated into a source file: a generated file would have to be
 * gitignored, and then a typecheck run before the build would fail on a fresh clone.
 */
declare const __BUILD_ID__: string
