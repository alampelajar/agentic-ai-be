import { useEffect, useRef, type ReactNode } from 'react'
import { reducedMotion } from './motion'

type ParallaxProps = {
  children?: ReactNode
  /** How much slower than the page it moves, from 0.05 to 0.3. Default 0.15. */
  speed?: number
  className?: string
}

/** A picture or decoration that moves a little slower than the page as it scrolls. */
export function Parallax({ children, speed = 0.15, className }: ParallaxProps) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const element = ref.current
    if (!element || reducedMotion()) return
    const factor = Math.min(0.3, Math.max(0.05, speed))
    let frame = 0
    const update = () => {
      frame = 0
      const box = element.getBoundingClientRect()
      const fromCentre = box.top + box.height / 2 - window.innerHeight / 2
      element.style.transform = `translate3d(0, ${(-fromCentre * factor).toFixed(1)}px, 0)`
    }
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update)
    }
    update()
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll)
    return () => {
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [speed])
  return (
    <div ref={ref} className={className} style={{ willChange: 'transform' }}>
      {children}
    </div>
  )
}

export default Parallax
