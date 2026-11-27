import { useEffect, useRef, useState, type ElementType } from 'react'
import { reducedMotion, useInView } from './motion'

type TypewriterProps = {
  /** The words to type, one short sentence or a headline. */
  text: string
  /** How fast the letters appear. Default 'normal'. */
  speed?: 'slow' | 'normal' | 'fast'
  /** Wait in milliseconds before typing starts, at most 1500. */
  delay?: number
  /** The element to render, e.g. 'h1', 'h2' or 'p'. Default 'span'. */
  as?: ElementType
  className?: string
}

const STEP = { slow: 90, normal: 55, fast: 30 }

/**
 * Words that appear letter by letter, as if typed, the first time they are scrolled
 * into view. The full text keeps its place from the start, so nothing below jumps,
 * and screen readers and visitors who turned movement off get the whole text at once.
 */
export function Typewriter({ text, speed = 'normal', delay = 0, as: Tag = 'span', className }: TypewriterProps) {
  const ref = useRef<HTMLElement>(null)
  const full = (text ?? '').trim()
  const [shown, setShown] = useState<number | null>(null)
  const [started, setStarted] = useState(false)
  useInView(ref, () => setStarted(true))

  useEffect(() => {
    if (!started) return
    if (reducedMotion()) {
      setShown(full.length)
      return
    }
    let count = 0
    let timer: ReturnType<typeof setInterval> | undefined
    const wait = setTimeout(() => {
      timer = setInterval(() => {
        count += 1
        setShown(count)
        if (count >= full.length && timer) clearInterval(timer)
      }, STEP[speed] ?? STEP.normal)
    }, Math.min(1500, Math.max(0, delay)))
    return () => {
      clearTimeout(wait)
      if (timer) clearInterval(timer)
    }
  }, [started, full, speed, delay])

  if (!full) return null
  // Before it starts (and when rendered on the server) the whole text is there, so a
  // page without JavaScript still reads.
  const typing = shown !== null && shown < full.length
  return (
    <Tag ref={ref} className={className}>
      <span className='sr-only'>{full}</span>
      <span aria-hidden='true' className='relative inline-block'>
        <span className={shown === null ? undefined : 'invisible'}>{full}</span>
        {shown === null ? null : (
          <span className='absolute inset-0'>
            {full.slice(0, shown)}
            {typing ? <span className='type-caret'>|</span> : null}
          </span>
        )}
      </span>
    </Tag>
  )
}

export default Typewriter
