import { useEffect, useState } from 'react'
import { reducedMotion } from './motion'

type RotatingWordsProps = {
  /** Two to six short words or phrases that take turns, e.g. ['roti', 'kue', 'pesanan']. */
  words: string[]
  /** Milliseconds each word stays. Default 2600. */
  interval?: number
  className?: string
}

/** One word of a headline that changes every few seconds. Screen readers hear them all. */
export function RotatingWords({ words, interval = 2600, className }: RotatingWordsProps) {
  const list = (words ?? []).filter((word) => word && word.trim()).slice(0, 6)
  const [index, setIndex] = useState(0)
  useEffect(() => {
    if (list.length < 2 || reducedMotion()) return
    const timer = setInterval(() => setIndex((current) => (current + 1) % list.length), Math.max(1500, interval))
    return () => clearInterval(timer)
  }, [list.length, interval])
  if (list.length === 0) return null
  return (
    <span className={className ?? 'text-primary'}>
      <span className='sr-only'>{list.join(', ')}</span>
      <span key={index} aria-hidden='true' className='word-swap inline-block'>
        {list[index % list.length]}
      </span>
    </span>
  )
}

export default RotatingWords
