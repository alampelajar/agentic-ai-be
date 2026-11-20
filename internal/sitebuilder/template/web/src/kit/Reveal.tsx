import { useRef, type CSSProperties, type ElementType, type ReactNode } from 'react'
import { useInView } from './motion'

type RevealProps = {
  children?: ReactNode
  /** Where the block comes from: up (rises), down, left, right, fade (only fades) or zoom. */
  from?: 'up' | 'down' | 'left' | 'right' | 'fade' | 'zoom'
  /** Extra wait in milliseconds before it moves, at most 600. */
  delay?: number
  /** Its place in a row of blocks that appear one after another (Stagger sets it). */
  order?: number
  /** The element to render, e.g. 'section', 'li' or 'h2'. Default 'div'. */
  as?: ElementType
  className?: string
  id?: string
}

/** A block that fades in and moves into place when it is scrolled into view. */
export function Reveal({ children, from = 'up', delay, order, as: Tag = 'div', className, id }: RevealProps) {
  const ref = useRef<HTMLElement>(null)
  useInView(ref, () => ref.current?.setAttribute('data-shown', ''))
  let style: CSSProperties | undefined
  if (delay) style = { transitionDelay: `${Math.min(600, Math.max(0, delay))}ms` }
  else if (order) style = { transitionDelay: `calc(var(--motion-stagger) * ${Math.min(order, 12)})` }
  return (
    <Tag ref={ref} id={id} data-reveal={from} style={style} className={className}>
      {children}
    </Tag>
  )
}

export default Reveal
