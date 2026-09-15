export type AdminTaskTabStat = {
  icon: string;
  value: string | number;
  label: string;
  tone?: "blue" | "red" | "purple" | "amber";
  refresh?: boolean;
};
