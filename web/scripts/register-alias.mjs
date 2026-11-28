// Usage: node --import ./scripts/register-alias.mjs --test ...
import { register } from "node:module";

register("./alias-loader.mjs", import.meta.url);
