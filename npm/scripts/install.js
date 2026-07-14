#!/usr/bin/env node

"use strict";

if (process.env.AIROUTE_NPM_SKIP_DOWNLOAD === "1") {
  process.stdout.write("Skipping AI Router binary download.\n");
  process.exit(0);
}

require("../lib/install").install().catch((error) => {
  process.stderr.write(`air: ${error.message}\n`);
  process.exit(1);
});
