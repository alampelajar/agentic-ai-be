import { site } from '../site'

/** The owner's photo at this place in the list (0 is the first), or null when there is none. */
export function firstPhoto(index = 0): { src: string; alt: string } | null {
  return site.images[index] ?? null
}
