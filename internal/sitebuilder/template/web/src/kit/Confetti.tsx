import { useEffect, useState, type CSSProperties } from 'react'
import { reducedMotion } from './motion'

type ConfettiProps = {
  /** What falls: flower petals, little pieces of paper, or hearts. Default 'petals'. */
  kind?: 'petals' | 'paper' | 'hearts'
  /** How many pieces, 8 to 40. Default 24. */
  count?: number
  /** Start falling only after this event on window, e.g. 'invitation-open' from InvitationCover. */
  after?: string
}

type Piece = { left: number; size: number; delay: number; duration: number; drift: number; spin: number; tone: number }

const TONES = ['text-accent', 'text-primary', 'text-accent/70', 'text-primary/60']

function Shape({ kind }: { kind: ConfettiProps['kind'] }) {
  if (kind === 'hearts') {
    return (
      <svg viewBox='0 0 24 24' className='size-full fill-current'>
        <path d='M12 21s-7.5-4.6-9.6-9.2C.9 8.4 3 4.5 6.7 4.5c2.1 0 3.6 1.1 4.3 2.6h2c.7-1.5 2.2-2.6 4.3-2.6 3.7 0 5.8 3.9 4.3 7.3C19.5 16.4 12 21 12 21z' />
      </svg>
    )
  }
  if (kind === 'paper') {
    return <span className='block size-full rounded-sm bg-current' />
  }
  return (
    <svg viewBox='0 0 24 24' className='size-full fill-current'>
      <path d='M12 2c4 3.5 6 7 6 10.5S15.3 22 12 22s-6-6-6-9.5S8 5.5 12 2z' />
    </svg>
  )
}

/**
 * Petals, paper or hearts that fall once over the page when it opens (or after
 * InvitationCover is opened), then disappear. Nothing can be clicked through them,
 * and visitors who turned movement off never see them.
 */
export function Confetti({ kind = 'petals', count = 24, after }: ConfettiProps) {
  const [pieces, setPieces] = useState<Piece[]>([])

  useEffect(() => {
    if (reducedMotion()) return
    let clear: ReturnType<typeof setTimeout> | undefined
    const start = () => {
      const total = Math.min(40, Math.max(8, Math.round(count)))
      const made: Piece[] = Array.from({ length: total }, () => ({
        left: Math.random() * 100,
        size: 10 + Math.random() * 12,
        delay: Math.random() * 2.5,
        duration: 5 + Math.random() * 3,
        drift: (Math.random() - 0.5) * 160,
        spin: (Math.random() - 0.5) * 720,
        tone: Math.floor(Math.random() * TONES.length),
      }))
      setPieces(made)
      clear = setTimeout(() => setPieces([]), 11000)
    }
    if (after) {
      window.addEventListener(after, start, { once: true })
    } else {
      start()
    }
    return () => {
      if (after) window.removeEventListener(after, start)
      if (clear) clearTimeout(clear)
    }
  }, [kind, count, after])

  if (pieces.length === 0) return null
  return (
    <div aria-hidden='true' className='pointer-events-none fixed inset-0 z-40 overflow-hidden'>
      {pieces.map((piece, index) => (
        <span
          key={index}
          className={`fall-piece absolute -top-8 ${TONES[piece.tone]}`}
          style={
            {
              left: `${piece.left}%`,
              width: `${piece.size}px`,
              height: `${piece.size}px`,
              animationDelay: `${piece.delay}s`,
              animationDuration: `${piece.duration}s`,
              '--fall-drift': `${piece.drift}px`,
              '--fall-spin': `${piece.spin}deg`,
            } as CSSProperties
          }
        >
          <Shape kind={kind} />
        </span>
      ))}
    </div>
  )
}

export default Confetti
