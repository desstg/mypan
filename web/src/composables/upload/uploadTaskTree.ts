import type { UploadTask } from "@/types/upload";

export type UploadTaskTreeNode = {
  id: string;
  name: string;
  batchId: string;
  path: string;
  isFolder: boolean;
  tasks: UploadTask[];
};

function cleanPath(value: string) {
  return String(value || "")
    .replace(/\\/g, "/")
    .split("/")
    .filter((part) => part && part !== ".")
    .join("/");
}

function taskRelativePath(task: UploadTask) {
  const rel = cleanPath(task.rel_path || "");
  if (rel) return rel;
  const name = cleanPath(task.file_name || "");
  const batchName = cleanPath(task.batch_name || "");
  if (batchName && name.startsWith(batchName + "/")) return name.slice(batchName.length + 1);
  return name || String(task.task_id);
}

export function buildUploadTaskLevel(
  tasks: UploadTask[],
  batchId = "",
  currentPath = "",
): UploadTaskTreeNode[] {
  if (!batchId) {
    const batches = new Map<string, UploadTask[]>();
    const nodes: UploadTaskTreeNode[] = [];
    for (const task of tasks) {
      const id = String(task.batch_id || "").trim();
      if (!id) {
        nodes.push({
          id: `task:${task.task_id}`,
          name: task.file_name,
          batchId: "",
          path: "",
          isFolder: false,
          tasks: [task],
        });
        continue;
      }
      const members = batches.get(id) || [];
      members.push(task);
      batches.set(id, members);
    }
    for (const [id, members] of batches) {
      nodes.push({
        id: `batch:${id}`,
        name: String(members[0]?.batch_name || "文件夹上传"),
        batchId: id,
        path: "",
        isFolder: true,
        tasks: members,
      });
    }
    return nodes;
  }

  const prefix = cleanPath(currentPath);
  const folders = new Map<string, UploadTask[]>();
  const files: UploadTaskTreeNode[] = [];
  for (const task of tasks) {
    if (String(task.batch_id || "") !== batchId) continue;
    if (task.batch_placeholder) continue;
    const relPath = taskRelativePath(task);
    if (prefix && relPath !== prefix && !relPath.startsWith(prefix + "/")) continue;
    const rest = prefix ? relPath.slice(prefix.length).replace(/^\/+/, "") : relPath;
    if (!rest) continue;
    const splitAt = rest.indexOf("/");
    if (splitAt < 0) {
      files.push({
        id: `task:${task.task_id}`,
        name: rest,
        batchId,
        path: prefix,
        isFolder: false,
        tasks: [task],
      });
      continue;
    }
    const name = rest.slice(0, splitAt);
    const path = cleanPath(prefix ? `${prefix}/${name}` : name);
    const members = folders.get(path) || [];
    members.push(task);
    folders.set(path, members);
  }
  const folderNodes = [...folders].map(([path, members]) => ({
    id: `folder:${batchId}:${path}`,
    name: path.split("/").pop() || path,
    batchId,
    path,
    isFolder: true,
    tasks: members,
  }));
  return [...folderNodes, ...files];
}

export function uploadTaskPathParts(path: string) {
  const parts = cleanPath(path).split("/").filter(Boolean);
  return parts.map((name, index) => ({ name, path: parts.slice(0, index + 1).join("/") }));
}
