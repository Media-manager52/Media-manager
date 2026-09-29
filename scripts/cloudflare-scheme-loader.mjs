// The production bundle targets the Cloudflare Workers runtime and imports the
// "cloudflare:workers" virtual module for optional tracing (only accessed via
// Reflect.get). Node cannot resolve that scheme, so both the rendered-html test
// and the production UI runtime register this loader to provide an inert
// module. See scripts/register-cloudflare-loader.mjs.
export async function resolve(specifier, context, nextResolve) {
  if (specifier.startsWith("cloudflare:")) {
    return { url: specifier, shortCircuit: true, format: "module" };
  }
  return nextResolve(specifier, context);
}

export async function load(url, context, nextLoad) {
  if (url.startsWith("cloudflare:")) {
    return {
      format: "module",
      shortCircuit: true,
      source:
        "const inert = {};\nexport default inert;\nexport const env = inert;\nexport const tracing = undefined;\n",
    };
  }
  return nextLoad(url, context);
}
