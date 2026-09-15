export type SortKey = "name" | "size" | "modified";
export type SortOrder = "asc" | "desc";

// 文件行进行中的操作类型，驱动行内「处理中」转圈与文案。
export type FileRowOperation = "delete" | "rename" | "move" | "copy";

export const rowOperationText: Record<FileRowOperation, string> = {
  delete: "删除中",
  rename: "重命名中",
  move: "移动中",
  copy: "复制中",
};
