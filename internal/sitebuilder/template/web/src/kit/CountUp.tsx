import { useEffect, useRef, useState } from 'react'
import { site } from '../site'
import { reducedMotion, useInView } from './motion'

type CountUpProps = {
  /** The number to reach. Only a number the owner gave; never an invented one. */
  to: number
  prefix?: string
  suffix?: string
  /** How long the counting takes in milliseconds. Default 1400. */
  duration?: number
  className?: string
}

const format = (value: number) => Math.round(value).toLocaleString(site.language === 'id' ? 'id-ID' : 'en-US')

/** A number that counts up from zero when it is scrolled into view. */
export function CountUp({ to, prefix = '', suffix = '', duration = 1400, className }: CountUpProps) {
  const ref = useRef<HTMLSpanElement>(null)
  const [value, setValue] = useState(to)
  const [counting, setCounting] = useState(false)

  useEffect(() => {
    if (!reducedMotion()) setValue(0)
  }, [to])

  useInView(ref, () => {
    if (!reducedMotion()) setCounting(true)
  })

  useEffect(() => {
    if (!counting) return
    const start = performance.now()
    const length = Math.min(4000, Math.max(300, duration))
    let frame = 0
    const step = (now: number) => {
      const progress = Math.min(1, (now - start) / length)
      setValue(to * (1 - Math.pow(1 - progress, 3)))
      if (progress < 1) frame = requestAnimationFrame(step)
    }
    frame = requestAnimationFrame(step)
    return () => cancelAnimationFrame(frame)
  }, [counting, to, duration])

  return (
    <span ref={ref} className={className ?? 'tabular-nums'}>
      <span className='sr-only'>
        {prefix}
        {format(to)}
        {suffix}
      </span>
      <span aria-hidden='true'>
        {prefix}
        {format(value)}
        {suffix}
      </span>
    </span>
  )
}

export default CountUp
