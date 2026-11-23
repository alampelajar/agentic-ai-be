import { look } from '../look'
import { Reveal } from '../kit/Reveal'
import { container, cx } from './ui'

type PageIntroProps = {
  /** The page's title, e.g. "Menu kami". It becomes the page's h1. */
  title: string
  /** One or two sentences about what this page offers. */
  text?: string
}

/** The opening of every page that is not the home page: its title and a short introduction. */
export function PageIntro({ title, text }: PageIntroProps) {
  const centred = look.centered
  return (
    <section className='border-b border-border bg-background text-foreground'>
      <Reveal from='up' className={cx(container, 'py-16 md:py-24', centred && 'text-center')}>
        {look.rule ? <span aria-hidden='true' className={cx('mb-6 block h-1.5 w-14 bg-accent', centred && 'mx-auto')} /> : null}
        <h1 className={cx('max-w-4xl font-heading text-4xl leading-tight tracking-tight md:text-6xl', centred && 'mx-auto', look.caps && 'font-black uppercase', look.hero === 'calm' && 'font-normal')}>{title}</h1>
        {text ? <p className={cx('mt-6 max-w-2xl text-lg leading-relaxed text-muted md:text-xl', centred && 'mx-auto')}>{text}</p> : null}
      </Reveal>
    </section>
  )
}

export default PageIntro
