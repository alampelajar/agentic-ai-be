import { Check } from 'lucide-react'
import type { ReactNode } from 'react'
import { look } from '../look'
import { Reveal } from '../kit/Reveal'
import type { Action } from './types'
import { Actions, container, cx, firstPhoto, frame } from './ui'

type HeroProps = {
  /** The main line: what this is, in at most about eight words. It becomes the page's h1. */
  title: string
  /** One word of the title to set apart (used only by looks that set a word apart). */
  highlight?: string
  /** One or two sentences under the title that say plainly what is offered and for whom. */
  text: string
  /** One or two buttons; the first is the main one. */
  actions?: Action[]
  /** Up to four short facts from the brief: opening hours, area served, what is made. */
  facts?: (string | { label?: string; value?: string })[]
  /** A short heading for the panel beside the title (looks with a panel). */
  panelTitle?: string
  /** Show the owner's first photo when there is one. Default true. */
  photo?: boolean
}

function Title({ title, highlight, className }: { title: string; highlight?: string; className: string }) {
  const caps = look.caps ? 'uppercase' : ''
  if (look.hero === 'pop' && highlight && title.includes(highlight)) {
    const [before, ...rest] = title.split(highlight)
    return (
      <h1 className={cx(className, caps)}>
        {before}
        <span className='inline-block rounded-full bg-accent px-4 text-on-accent'>{highlight}</span>
        {rest.join(highlight)}
      </h1>
    )
  }
  return <h1 className={cx(className, caps)}>{title}</h1>
}

function FactList({ facts, className }: { facts: string[]; className?: string }) {
  if (facts.length === 0) return null
  return (
    <ul className={cx('space-y-3', className)}>
      {facts.map((fact) => (
        <li key={fact} className='flex items-start gap-3 text-base md:text-lg'>
          <Check className='mt-1 size-5 shrink-0 text-accent' aria-hidden='true' />
          <span>{fact}</span>
        </li>
      ))}
    </ul>
  )
}

function Photo({ className }: { className: string }) {
  const photo = firstPhoto()
  if (!photo) return null
  return <img src={photo.src} alt={photo.alt} className={cx('w-full object-cover', className)} />
}

/** The opening of the home page. The look decides its shape; the page brings the words. */
export function Hero({ title, highlight, text, actions, facts = [], panelTitle, photo = true }: HeroProps) {
  const shown = facts
    .map((fact) => (typeof fact === 'string' ? fact : [fact?.label, fact?.value].filter(Boolean).join(': ')))
    .filter(Boolean)
    .slice(0, 4)
  const hasPhoto = photo && firstPhoto() !== null
  const lead = 'text-lg leading-relaxed text-muted md:text-xl'
  let body: ReactNode

  switch (look.hero) {
    case 'calm':
      body = (
        <div className={cx(container, 'py-24 text-center md:py-36')}>
          <Reveal from='fade' className='mx-auto max-w-3xl'>
            <Title title={title} highlight={highlight} className='font-heading text-5xl font-normal leading-tight tracking-tight md:text-6xl' />
            <p className={cx('mx-auto mt-6 max-w-xl', lead)}>{text}</p>
            <Actions actions={actions} className='mt-10 justify-center' />
          </Reveal>
          {hasPhoto ? (
            <Reveal from='fade' delay={200} className='mx-auto mt-16 max-w-4xl'>
              <Photo className='aspect-[16/9]' />
            </Reveal>
          ) : null}
        </div>
      )
      break
    case 'split':
      body = (
        <div className={cx(container, 'grid items-center gap-12 py-16 md:grid-cols-2 md:py-24')}>
          <Reveal from='up'>
            <Title title={title} highlight={highlight} className='font-heading text-4xl font-semibold leading-tight tracking-tight md:text-6xl' />
            <p className={cx('mt-6 max-w-xl', lead)}>{text}</p>
            <Actions actions={actions} className='mt-10' />
          </Reveal>
          <Reveal from='right' delay={150} className={cx('rounded-xl bg-surface p-8 md:p-10', frame())}>
            {hasPhoto && shown.length === 0 ? (
              <Photo className='aspect-[4/3] rounded-lg' />
            ) : (
              <>
                {panelTitle ? <h2 className={cx('mb-6 font-heading text-2xl', look.caps && 'uppercase')}>{panelTitle}</h2> : null}
                <FactList facts={shown} />
                {hasPhoto ? <Photo className='mt-8 aspect-[16/9] rounded-lg' /> : null}
              </>
            )}
          </Reveal>
        </div>
      )
      break
    case 'poster':
      body = (
        <div className={cx(container, 'py-20 md:py-32')}>
          <Reveal from='up'>
            <Title title={title} highlight={highlight} className='max-w-5xl font-heading text-5xl font-black leading-none tracking-tight sm:text-6xl md:text-8xl' />
          </Reveal>
          <Reveal from='up' delay={150} className='mt-8 grid gap-8 md:grid-cols-12 md:items-end'>
            <div className='md:col-span-7'>
              <p className={cx('max-w-2xl', lead)}>{text}</p>
              {shown.length ? <p className='mt-4 text-base font-semibold'>{shown.join(' / ')}</p> : null}
            </div>
            <Actions actions={actions} className='md:col-span-5 md:justify-end' />
          </Reveal>
          {hasPhoto ? (
            <Reveal from='fade' delay={250} className='mt-14'>
              <Photo className='aspect-[21/9]' />
            </Reveal>
          ) : null}
        </div>
      )
      break
    case 'pop':
      body = (
        <div className={cx(container, 'py-20 text-center md:py-28')}>
          <Reveal from='zoom'>
            <Title title={title} highlight={highlight} className='mx-auto max-w-4xl font-heading text-5xl font-extrabold leading-tight tracking-tight md:text-7xl' />
            <p className={cx('mx-auto mt-6 max-w-2xl', lead)}>{text}</p>
            <Actions actions={actions} className='mt-10 justify-center' />
          </Reveal>
          {shown.length ? (
            <Reveal from='up' delay={200} className='mt-10 flex flex-wrap justify-center gap-3'>
              {shown.map((fact) => (
                <span key={fact} className='rounded-full border-2 border-border bg-surface px-4 py-2 text-sm font-semibold'>
                  {fact}
                </span>
              ))}
            </Reveal>
          ) : null}
          {hasPhoto ? (
            <Reveal from='zoom' delay={250} className='mx-auto mt-14 max-w-4xl'>
              <Photo className='aspect-[16/9] rounded-2xl border-2 border-border' />
            </Reveal>
          ) : null}
        </div>
      )
      break
    case 'panel':
      body = (
        <div className={cx(container, 'py-10 md:py-16')}>
          <Reveal from='up' className='grid items-center gap-10 rounded-3xl bg-surface p-8 sm:p-12 md:grid-cols-2 md:p-16'>
            <div>
              <Title title={title} highlight={highlight} className='font-heading text-5xl leading-tight tracking-tight md:text-7xl' />
              <p className={cx('mt-6 max-w-xl', lead)}>{text}</p>
              <Actions actions={actions} className='mt-10' />
            </div>
            {hasPhoto ? <Photo className='aspect-[4/5] rounded-3xl md:rounded-tl-none' /> : <FactList facts={shown} className='md:pl-8' />}
          </Reveal>
        </div>
      )
      break
    case 'framed':
      body = (
        <div className={cx(container, 'py-16 md:py-24')}>
          <Reveal from='fade' className={cx('mx-auto max-w-4xl rounded-2xl px-6 py-16 text-center sm:px-12 md:py-24', frame() || 'border border-dashed border-border')}>
            <Title title={title} highlight={highlight} className='font-heading text-4xl italic leading-tight tracking-tight md:text-6xl' />
            <p className={cx('mx-auto mt-6 max-w-xl', lead)}>{text}</p>
            <Actions actions={actions} className='mt-10 justify-center' />
            {shown.length ? <p className='mt-8 text-sm text-muted'>{shown.join(' · ')}</p> : null}
          </Reveal>
        </div>
      )
      break
    default:
      // editorial: a very large headline over two thirds, the words and buttons below, a photo beside.
      body = (
        <div className={cx(container, 'border-b border-border py-20 md:py-28')}>
          <div className='grid gap-12 md:grid-cols-12'>
            <Reveal from='up' className={hasPhoto ? 'md:col-span-8' : 'md:col-span-10'}>
              <Title title={title} highlight={highlight} className='font-heading text-5xl leading-none tracking-tight md:text-7xl' />
              <p className={cx('mt-8 max-w-2xl', lead)}>{text}</p>
              <Actions actions={actions} className='mt-10' />
              {shown.length ? <p className='mt-10 text-sm text-muted'>{shown.join(' · ')}</p> : null}
            </Reveal>
            {hasPhoto ? (
              <Reveal from='fade' delay={200} className='md:col-span-4'>
                <Photo className='aspect-[3/4]' />
              </Reveal>
            ) : null}
          </div>
        </div>
      )
  }

  return <section className='bg-background text-foreground'>{body}</section>
}

export default Hero
