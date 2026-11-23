import { look } from '../look'
import { Reveal } from '../kit/Reveal'
import { Ornament } from '../kit/Ornament'
import { container, cx } from './ui'

type StatementProps = {
  /** One strong sentence: what makes the place special, a promise the owner made, or their own words. At most about 25 words. */
  text: string
  /** Who said it, only when the owner's own words are quoted. */
  by?: string
}

/** One sentence set large, once on a page: a band of colour or a quotation, as the look decides. */
export function Statement({ text, by }: StatementProps) {
  if (!text) return null
  if (look.statement === 'band') {
    return (
      <section className='bg-primary text-primary-foreground'>
        <Reveal from='up' className={cx(container, 'py-20 md:py-28')}>
          <p className={cx('max-w-4xl font-heading text-3xl leading-tight tracking-tight md:text-5xl', look.caps && 'uppercase')}>{text}</p>
          {by ? <p className='mt-6 text-lg opacity-80'>{by}</p> : null}
        </Reveal>
      </section>
    )
  }
  return (
    <section className='bg-background text-foreground'>
      <Reveal from='fade' className={cx(container, 'flex flex-col items-center py-20 text-center md:py-28')}>
        <Ornament kind={look.frame === 'dashed' ? 'dots' : 'line'} className='mb-10 w-24 text-accent' />
        <blockquote className='max-w-3xl'>
          <p className='font-heading text-3xl italic leading-snug md:text-4xl'>{text}</p>
          {by ? <footer className='mt-6 text-base text-muted'>{by}</footer> : null}
        </blockquote>
        <Ornament kind={look.frame === 'dashed' ? 'dots' : 'line'} className='mt-10 w-24 text-accent' />
      </Reveal>
    </section>
  )
}

export default Statement
