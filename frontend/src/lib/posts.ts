import type { Post } from '@/lib/api'

// Dynamic posts often put hashtags or the entire body in the title field.
// Keep the first content line as the heading; the reader still has the full body.
export function postTitle(post: Post): string {
  const title = post.title
    .replace(/#[^#\n]+#/g, '')
    .replace(/\u200b/g, '')
    .trim()
  const line =
    title
      .split('\n')
      .map((part) => part.trim())
      .find(Boolean) ||
    post.title.trim() ||
    '官方动态'
  const characters = Array.from(line)
  return characters.length > 90 ? `${characters.slice(0, 90).join('')}…` : line
}
