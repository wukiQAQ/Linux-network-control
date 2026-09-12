import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

// 背景：v0.5.4 中 App.vue 的模板写了 @update-mode="setChartMode"，
// 但脚本里没有定义 setChartMode，导致面积图/柱状图点击无效。
// 这个测试静态检查「模板里引用的事件处理器必须在脚本里定义」，防止同类问题再次出现。

const here = dirname(fileURLToPath(import.meta.url));
const srcDir = join(here, "..", "src");

// 属于框架/模板自身的写法，不需要在脚本中定义
const BUILTIN = new Set(["$emit", "$event", "$set", "$refs", "Math", "Number", "String", "Boolean"]);

function templateOf(text) {
  const m = text.match(/<template>([\s\S]*)<\/template>/);
  return m ? m[1] : "";
}

function scriptOf(text) {
  const m = text.match(/<script setup>([\s\S]*?)<\/script>/);
  return m ? m[1] : "";
}

function definedNames(script) {
  const names = new Set();
  const add = (re) => {
    let m;
    while ((m = re.exec(script)) !== null) names.add(m[1].trim());
  };
  add(/\bfunction\s+([A-Za-z_$][\w$]*)/g);
  add(/\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)/g);
  // import { a, b as c } from "..."
  const imports = script.matchAll(/import\s*\{([^}]*)\}\s*from/g);
  for (const m of imports) {
    m[1].split(",").forEach((part) => {
      const name = part.trim().split(/\s+as\s+/).pop();
      if (name) names.add(name.trim());
    });
  }
  add(/import\s+([A-Za-z_$][\w$]*)\s+from/g);
  return names;
}

function handlerNames(template) {
  const names = new Set();
  const re = /@[A-Za-z:.-]+\s*=\s*"([^"]+)"/g;
  let m;
  while ((m = re.exec(template)) !== null) {
    const expr = m[1].trim();
    const id = expr.match(/^([A-Za-z_$][\w$]*)/);
    if (!id) continue;
    if (BUILTIN.has(id[1])) continue;
    // 形如 p.base / item.value 的表达式不是处理器调用，跳过以「.」开头成员访问的情况
    names.add(id[1]);
  }
  return names;
}

function vueFiles(dir) {
  const out = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) out.push(...vueFiles(full));
    else if (entry.name.endsWith(".vue")) out.push(full);
  }
  return out;
}

const files = vueFiles(srcDir);
assert.ok(files.length >= 4, "未找到 Vue 组件文件");

for (const file of files) {
  test(`模板事件处理器均有定义：${file.split(/[\\/]/).slice(-2).join("/")}`, () => {
    const text = readFileSync(file, "utf8");
    const defined = definedNames(scriptOf(text));
    const missing = [...handlerNames(templateOf(text))].filter((n) => !defined.has(n));
    assert.deepEqual(missing, [], "模板引用了未定义的处理函数: " + missing.join(", "));
  });
}

test("App.vue 定义了图表切换函数 setChartMode", () => {
  const app = readFileSync(join(srcDir, "App.vue"), "utf8");
  assert.ok(/function\s+setChartMode\s*\(/.test(scriptOf(app)), "缺少 setChartMode 定义");
  assert.ok(
    app.includes('localStorage.setItem(CHART_KEY, m)'),
    "图表模式未写入本地存储",
  );
});