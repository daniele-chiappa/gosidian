/**
 * A frontmatter key as the properties panel shows it: `open_plans` reads
 * "Open plans", `dueDate` "Due date". The key itself stays in the tooltip;
 * one in capitals (`IMP`, `URL`) keeps them.
 */
export function fieldLabel(key: string): string {
  const words = key
    .replace(/([a-z0-9])([A-Z])/g, (_, a: string, b: string) => `${a} ${b.toLowerCase()}`)
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : key
}
