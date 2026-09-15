export type UploadTaskStatus =
  | "pending"
  | "running"
  | "paused"
  | "success"
  | "failed"
  | "canceled"
  | "skipped";

export interface UploadTask {
  task_id: string;
  client_task_id?: string;
  batch_id?: string;
  batch_name?: string;
  batch_placeholder?: boolean;
  account_id: number;
  account_name?: string;
  driver_type?: string;
  file_name: string;
  source_type?: string;
  source_account_id?: number;
  source_account_name?: string;
  source_driver_type?: string;
  source_file_id?: string;
  rel_path?: string;
  rel_dir?: string;
  target_path: string;
  target_display_path?: string;
  status: UploadTaskStatus;
  phase?: string;
  progress: number;
  downloaded_bytes?: number;
  uploaded_bytes?: number;
  speed_bytes_per_second?: number;
  total_bytes?: number;
  message?: string;
  error?: string;
  result?: {
    file_id?: string;
    parent_id?: string;
    parent_path?: string;
    file_name?: string;
    size?: number;
    batch_root_id?: string;
    batch_root_parent_id?: string;
    batch_root_owned?: boolean;
  };
  queue_order?: number;
  created_at?: number;
  updated_at?: number;
}

export interface BatchDeleteUploadResult {
  deleted_task_ids: string[];
  failed_task_ids: string[];
  missing_task_ids: string[];
  failed_messages: Record<string, string>;
}
