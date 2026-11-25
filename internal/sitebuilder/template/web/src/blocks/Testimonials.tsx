import { Quote } from 'lucide-react'
import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame } from './ui'

type Testimonial = {
  /** What the customer said, word for word from the brief. Never invented. */
  text: string
  /** Their name as the brief gives it. */
  name: string
  /** Who they are, e.g. "Pelanggan sejak 2020", only from the brief. */
  role?: string
}

type TestimonialsProps = {
  /** The heading, e.g. "Kata pelanggan". */
  title: string
  intro?: string
  /** Only words of real customers that the owner gave. Leave the section out when there are none. */
  items: Testimonial[]
  tone?: Tone
  id?: string
}

/** What customers said, in their own words, as quotation cards. Only for words the owner gave. */
export function Testimonials({ title, intro, items, tone = 'auto', id }: TestimonialsProps) {
  const list = (items ?? []).filter((item) => item && item.text && item.name).slice(0, 6)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const shape = look.button === 'pill' ? 'rounded-3xl' : look.button === 'square' ? 'rounded-none' : 'rounded-xl'

  // One quotation: set large in the middle, like a statement.
  if (list.length === 1) {
    const [item] = list
    return (
      <Section tone={tone} id={id} labelledBy={headingId}>
        <SectionHeading id={headingId} title={title} intro={intro} tone={tone} center />
        <figure className='mx-auto flex max-w-3xl flex-col items-center gap-6 text-center'>
          <Quote className='size-10 text-accent' aria-hidden='true' />
          <blockquote className='font-heading text-2xl leading-snug md:text-4xl'>{item.text}</blockquote>
          <figcaption>
            <span className='block font-semibold'>{item.name}</span>
            {item.role ? <span className='block text-sm opacity-80'>{item.role}</span> : null}
          </figcaption>
        </figure>
      </Section>
    )
  }

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      <Stagger className={cx('grid gap-6 md:grid-cols-2', list.length % 3 === 0 && 'lg:grid-cols-3')}>
        {list.map((item, index) => (
          <figure key={item.name + index} className={cx('lift flex h-full flex-col gap-6 bg-surface p-8 text-foreground', shape, frame() || 'border border-border', look.tilt && (index % 2 ? 'md:rotate-1' : 'md:-rotate-1'))}>
            <Quote className='size-7 text-accent' aria-hidden='true' />
            <blockquote className='text-lg leading-relaxed'>{item.text}</blockquote>
            <figcaption className='mt-auto flex items-center gap-3 border-t border-border pt-5'>
              <span aria-hidden='true' className='flex size-10 shrink-0 items-center justify-center rounded-full bg-accent/15 font-heading text-lg text-primary'>
                {item.name.trim().charAt(0).toUpperCase()}
              </span>
              <span>
                <span className='block font-semibold'>{item.name}</span>
                {item.role ? <span className='block text-sm text-muted'>{item.role}</span> : null}
              </span>
            </figcaption>
          </figure>
        ))}
      </Stagger>
    </Section>
  )
}

export default Testimonials
