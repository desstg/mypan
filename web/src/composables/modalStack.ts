// 模块级弹窗栈：让 Escape 只关闭最上层弹窗（嵌套弹窗按一次只关一层）。
const stack: symbol[] = [];

export function pushModal(token: symbol) {
  if (!stack.includes(token)) stack.push(token);
}

export function popModal(token: symbol) {
  const index = stack.indexOf(token);
  if (index >= 0) stack.splice(index, 1);
}

export function isTopModal(token: symbol) {
  return stack[stack.length - 1] === token;
}
