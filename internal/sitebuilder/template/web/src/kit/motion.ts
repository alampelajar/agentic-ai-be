import { useEffect, type RefObject } from 'react'

// Shared by the motion pieces (Reveal, Stagger, CountUp, Marquee, RotatingWords,
// Parallax). How they move comes from the theme's --motion-* tokens; everything is
// shown at once, without movement, for visitors who turned movement off.

/** True when the visitor asked their device for less movement. */
export function reducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  )
}

/** Calls onEnter once, the first time the element is scrolled into view. */
export function useInView(ref: RefObject<Element | null>, onEnter: () => void) {
  useEffect(() => {
    const element = ref.current
    if (!element) return
    if (typeof IntersectionObserver === 'undefined') {
      onEnter()
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          observer.disconnect()
          onEnter()
        }
      },
      { rootMargin: '0px 0px -8% 0px' },
    )
    observer.observe(element)
    return () => observer.disconnect()
    // onEnter runs once; a new function on a later render must not observe again.
  }, [ref])
}
