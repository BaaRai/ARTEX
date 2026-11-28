// Node module hooks for running TypeScript tests with `node --test`: resolves the
// "@/..." path alias (tsconfig "paths") to src/, adding the ".ts" extension that the
// bundler normally infers. Registered by scripts/register-alias.mjs.

const SRC = new URL("../src/", import.meta.url);

export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("@/")) {
    const base = new URL(specifier.slice(2), SRC).href;
    for (const candidate of [base, `${base}.ts`, `${base}.tsx`, `${base}/index.ts`]) {
      try {
        return await nextResolve(candidate, context);
      } catch {
        // try the next candidate
      }
    }
  }
  return nextResolve(specifier, context);
}
