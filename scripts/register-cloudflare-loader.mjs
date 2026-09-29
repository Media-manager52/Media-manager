// Entry point for NODE_OPTIONS=--import. Registers the inert loader that lets
// the built Cloudflare Workers bundle run under the Node production server.
import { register } from "node:module";

register(new URL("./cloudflare-scheme-loader.mjs", import.meta.url));
