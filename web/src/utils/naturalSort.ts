export function naturalSort(a: string, b: string): number {
  const splitA = String(a).match(/(\d+|\D+)/g) || [];
  const splitB = String(b).match(/(\d+|\D+)/g) || [];
  const maxLength = Math.max(splitA.length, splitB.length);

  for (let i = 0; i < maxLength; i++) {
    const partA = splitA[i] || "";
    const partB = splitB[i] || "";

    if (/^\d+$/.test(partA) && /^\d+$/.test(partB)) {
      const numA = parseInt(partA, 10);
      const numB = parseInt(partB, 10);
      if (numA !== numB) return numA - numB;
    } else {
      const comparison = partA.toLowerCase().localeCompare(partB.toLowerCase(), "zh-CN");
      if (comparison !== 0) return comparison;
    }
  }

  return 0;
}
