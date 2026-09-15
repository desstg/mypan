import assert from "node:assert/strict";
import { performance } from "node:perf_hooks";
import { buildUploadTaskLevel } from "../src/composables/upload/uploadTaskTree.ts";

function task(index, relPath) {
  return {
    task_id: `task-${index}`,
    batch_id: "batch-demo",
    batch_name: "示例文件夹",
    account_id: 1,
    file_name: `示例文件夹/${relPath}`,
    rel_path: relPath,
    target_path: "root",
    status: "pending",
    progress: 0,
  };
}

const small = [
  task(1, "第一季/01.mp4"),
  task(2, "第一季/02.mp4"),
  task(3, "海报/poster.jpg"),
];
const root = buildUploadTaskLevel(small);
assert.equal(root.length, 1);
assert.equal(root[0].isFolder, true);
assert.equal(root[0].tasks.length, 3);
const firstLevel = buildUploadTaskLevel(small, "batch-demo", "");
assert.deepEqual(firstLevel.map((node) => node.name).sort(), ["海报", "第一季"]);
const episodes = buildUploadTaskLevel(small, "batch-demo", "第一季");
assert.deepEqual(episodes.map((node) => node.name), ["01.mp4", "02.mp4"]);

const placeholder = {
  ...task("preparing", ""),
  file_name: "示例文件夹",
  batch_placeholder: true,
};
const preparingRoot = buildUploadTaskLevel([placeholder]);
assert.equal(preparingRoot.length, 1);
assert.equal(preparingRoot[0].tasks[0].batch_placeholder, true);
assert.deepEqual(buildUploadTaskLevel([placeholder], "batch-demo", ""), []);

const many = Array.from({ length: 10_000 }, (_, index) => task(index, `第${index % 100}季/${index}.mkv`));
const started = performance.now();
const grouped = buildUploadTaskLevel(many);
const folders = buildUploadTaskLevel(many, "batch-demo", "");
const elapsed = performance.now() - started;
assert.equal(grouped.length, 1);
assert.equal(folders.length, 100);
assert.ok(elapsed < 1000, `万级任务聚合耗时过长：${elapsed.toFixed(1)}ms`);
console.log(`upload task tree ok: 10000 tasks in ${elapsed.toFixed(1)}ms`);
