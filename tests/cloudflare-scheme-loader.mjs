// The built server targets the Cloudflare Workers runtime and imports the
// "cloudflare:workers" virtual module for optional tracing. Node cannot resolve
// that scheme by default, so the rendered-html test registers this loader to
// provide an inert module for it.
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
