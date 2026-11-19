import type { CSSProperties } from 'react'

type MarqueeProps = {
  /** Short words or phrases: services, products, places. */
  items: string[]
  speed?: 'slow' | 'normal' | 'fast'
  /** What sits between the items. Default 'dot'. */
  separator?: 'dot' | 'star' | 'slash' | 'none'
  className?: string
  itemClassName?: string
}

const SECONDS = { slow: 60, normal: 40, fast: 24 }
const MARKS = { dot: '•', star: '✦', slash: '/', none: '' }

/** A line of words that slides slowly across the page and stops under the pointer. */
export function Marquee({ items, speed = 'normal', separator = 'dot', className, itemClassName }: MarqueeProps) {
  const words = (items ?? []).filter((item) => item && item.trim())
  if (words.length === 0) return null
  const mark = MARKS[separator] ?? MARKS.dot
  const row = (copy: boolean) =>
    words.map((word, index) => (
      <li
        key={`${copy ? 'b' : 'a'}${index}`}
        aria-hidden={copy || undefined}
        data-copy={copy || undefined}
        className='flex shrink-0 items-center gap-8'
      >
        <span className={itemClassName ?? 'font-heading text-2xl text-foreground md:text-4xl'}>{word}</span>
        {mark ? (
          <span aria-hidden='true' className='text-accent'>
            {mark}
          </span>
        ) : null}
      </li>
    ))
  return (
    <div className={className ?? 'overflow-hidden border-y border-border py-5'}>
      <ul
        className='marquee-track flex w-max gap-8'
        style={{ '--marquee-duration': `${SECONDS[speed] ?? SECONDS.normal}s` } as CSSProperties}
      >
        {row(false)}
        {row(true)}
      </ul>
    </div>
  )
}

export default Marquee
