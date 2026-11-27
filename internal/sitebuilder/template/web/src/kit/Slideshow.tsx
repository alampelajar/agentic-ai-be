import { useEffect, useRef, useState } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { site } from '../site'
import { reducedMotion } from './motion'
import { words } from './words'

type Slide = { src: string; alt: string; caption?: string }

type SlideshowProps = {
  /** Which of the owner's photos (indexes into site.images); all of them when left out. */
  photos?: number[]
  /** Short words under each photo, in the same order. */
  captions?: string[]
  /** Milliseconds each photo stays before the next one, 3000 to 10000. Default 5000. */
  interval?: number
  /** Shape of the frame. Default 'wide'. */
  shape?: 'wide' | 'square' | 'tall'
  className?: string
}

const SHAPES = { wide: 'aspect-video', square: 'aspect-square', tall: 'aspect-[4/5]' }

/**
 * The owner's photos one at a time. They move on by themselves, stop while the
 * pointer or the keyboard is on them, and can be swiped with a finger or moved with
 * the arrows and dots. Renders nothing without photos, and never moves by itself for
 * visitors who turned movement off.
 */
export function Slideshow({ photos, captions, interval = 5000, shape = 'wide', className }: SlideshowProps) {
  const slides: Slide[] = (photos ? photos.map((index) => site.images[index]) : site.images)
    .filter((photo): photo is { src: string; alt: string } => Boolean(photo && photo.src))
    .map((photo, index) => ({ ...photo, caption: captions?.[index] }))
  const track = useRef<HTMLUListElement>(null)
  const [current, setCurrent] = useState(0)
  const [paused, setPaused] = useState(false)

  const go = (index: number) => {
    const element = track.current
    if (!element || slides.length === 0) return
    const next = (index + slides.length) % slides.length
    element.scrollTo({ left: next * element.clientWidth, behavior: reducedMotion() ? 'auto' : 'smooth' })
  }

  // The dot follows the photo in view, also after a swipe.
  useEffect(() => {
    const element = track.current
    if (!element) return
    const onScroll = () => setCurrent(Math.round(element.scrollLeft / Math.max(1, element.clientWidth)))
    element.addEventListener('scroll', onScroll, { passive: true })
    return () => element.removeEventListener('scroll', onScroll)
  }, [])

  useEffect(() => {
    if (paused || slides.length < 2 || reducedMotion()) return
    const timer = setTimeout(() => go(current + 1), Math.min(10000, Math.max(3000, interval)))
    return () => clearTimeout(timer)
    // go reads the track itself; the timer starts again whenever the photo changes.
  }, [current, paused, slides.length, interval])

  if (slides.length === 0) return null
  const frame = SHAPES[shape] ?? SHAPES.wide
  const button =
    'absolute top-1/2 hidden size-10 -translate-y-1/2 items-center justify-center rounded-full border border-border bg-background/90 text-foreground transition-colors hover:bg-background focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary md:inline-flex'

  return (
    <div
      className={className ?? 'relative'}
      role='region'
      aria-roledescription='carousel'
      aria-label={words.photo}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
      onTouchStart={() => setPaused(true)}
    >
      <div className='relative'>
        <ul
          ref={track}
          className='flex snap-x snap-mandatory overflow-x-auto rounded-lg [scrollbar-width:none] [&::-webkit-scrollbar]:hidden'
        >
          {slides.map((slide, index) => (
            <li
              key={slide.src + index}
              className='w-full shrink-0 snap-center'
              aria-roledescription='slide'
              aria-label={`${index + 1} / ${slides.length}`}
            >
              <figure>
                <img
                  src={slide.src}
                  alt={slide.alt}
                  loading={index === 0 ? 'eager' : 'lazy'}
                  className={`${frame} w-full object-cover`}
                />
                {slide.caption ? <figcaption className='pt-3 text-center text-sm text-muted'>{slide.caption}</figcaption> : null}
              </figure>
            </li>
          ))}
        </ul>
        {slides.length > 1 ? (
          <>
            <button type='button' aria-label={words.previous} onClick={() => go(current - 1)} className={`${button} left-3`}>
              <ChevronLeft className='size-5' />
            </button>
            <button type='button' aria-label={words.next} onClick={() => go(current + 1)} className={`${button} right-3`}>
              <ChevronRight className='size-5' />
            </button>
          </>
        ) : null}
      </div>
      {slides.length > 1 ? (
        <div className='mt-4 flex justify-center gap-2'>
          {slides.map((slide, index) => (
            <button
              key={slide.src + index}
              type='button'
              aria-label={`${words.photo} ${index + 1}`}
              aria-current={index === current || undefined}
              onClick={() => go(index)}
              className={`h-2 rounded-full transition-all focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary ${
                index === current ? 'w-6 bg-primary' : 'w-2 bg-border hover:bg-muted'
              }`}
            />
          ))}
        </div>
      ) : null}
    </div>
  )
}

export default Slideshow
