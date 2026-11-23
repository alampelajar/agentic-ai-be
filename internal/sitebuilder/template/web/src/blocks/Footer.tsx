import { Clock, Mail, MapPin, MessageCircle, Phone } from 'lucide-react'
import type { ReactNode } from 'react'
import { look } from '../look'
import { Link } from '../router'
import { site, whatsappLink } from '../site'
import { container, cx } from './ui'

type FooterProps = {
  /** One short closing line in the site's language, e.g. a thank-you or an invitation to visit. */
  signOff?: string
}

const words = {
  id: { address: 'Alamat', hours: 'Jam buka', contact: 'Kontak', pages: 'Halaman', whatsapp: 'WhatsApp' },
  en: { address: 'Address', hours: 'Opening hours', contact: 'Contact', pages: 'Pages', whatsapp: 'WhatsApp' },
}

/** The close of every page: the name again, large, with the address, the hours and how to get in touch. Empty details are left out. */
export function Footer({ signOff }: FooterProps) {
  const t = words[site.language] ?? words.id
  const c = site.contact
  const wa = whatsappLink()
  const dark = look.footer === 'dark'
  const tone = dark ? 'bg-foreground text-background' : look.footer === 'centered' || look.footer === 'row' ? 'bg-background text-foreground' : 'bg-surface text-foreground'
  const muted = dark ? 'opacity-75' : 'text-muted'
  const year = new Date().getFullYear()

  const contactLines: { icon: ReactNode; content: ReactNode }[] = []
  if (wa) contactLines.push({ icon: <MessageCircle className='size-4' />, content: <a href={wa} target='_blank' rel='noreferrer' className='hover:underline'>{t.whatsapp}</a> })
  if (c.phone) contactLines.push({ icon: <Phone className='size-4' />, content: <a href={`tel:${c.phone.replace(/[^0-9+]/g, '')}`} className='hover:underline'>{c.phone}</a> })
  if (c.email) contactLines.push({ icon: <Mail className='size-4' />, content: <a href={`mailto:${c.email}`} className='break-all hover:underline'>{c.email}</a> })

  const line = (icon: ReactNode, content: ReactNode, key: string) => (
    <li key={key} className='flex items-start gap-3'>
      <span aria-hidden='true' className='mt-1 shrink-0 text-accent'>
        {icon}
      </span>
      <span>{content}</span>
    </li>
  )

  if (look.footer === 'centered' || look.footer === 'row') {
    const details = [c.address, c.hours, c.phone, c.email].filter(Boolean)
    return (
      <footer className={cx(tone, 'border-t border-border')}>
        <div className={cx(container, look.footer === 'centered' ? 'flex flex-col items-center gap-5 py-16 text-center' : 'flex flex-col gap-4 py-10 md:flex-row md:items-center md:justify-between')}>
          <p className={cx('font-heading tracking-tight', look.footer === 'centered' ? 'text-4xl md:text-5xl' : 'text-2xl', look.caps && 'uppercase')}>{site.name}</p>
          {signOff ? <p className={cx('max-w-xl text-lg', muted)}>{signOff}</p> : null}
          {details.length ? <p className={cx('text-sm', muted)}>{details.join(' · ')}</p> : null}
          {wa ? (
            <a href={wa} target='_blank' rel='noreferrer' className='inline-flex items-center gap-2 text-sm font-semibold underline decoration-accent decoration-2 underline-offset-8'>
              <MessageCircle className='size-4' aria-hidden='true' />
              {t.whatsapp}
            </a>
          ) : null}
          <p className={cx('text-xs', muted)}>
            © {year} {site.name}
          </p>
        </div>
      </footer>
    )
  }

  return (
    <footer className={tone}>
      <div className={cx(container, 'py-16 md:py-20')}>
        <p className={cx('font-heading text-4xl leading-none tracking-tight sm:text-6xl md:text-7xl', look.caps && 'font-black uppercase')}>{site.name}</p>
        {signOff ? <p className={cx('mt-5 max-w-2xl text-lg md:text-xl', muted)}>{signOff}</p> : null}
        <div className='mt-12 grid gap-10 border-t border-current/15 pt-10 sm:grid-cols-2 lg:grid-cols-4'>
          {c.address ? (
            <div>
              <h2 className={cx('mb-3 text-sm font-semibold', look.caps && 'uppercase tracking-wide')}>{t.address}</h2>
              <ul className='space-y-2'>{line(<MapPin className='size-4' />, c.address, 'address')}</ul>
            </div>
          ) : null}
          {c.hours ? (
            <div>
              <h2 className={cx('mb-3 text-sm font-semibold', look.caps && 'uppercase tracking-wide')}>{t.hours}</h2>
              <ul className='space-y-2'>{line(<Clock className='size-4' />, c.hours, 'hours')}</ul>
            </div>
          ) : null}
          {contactLines.length ? (
            <div>
              <h2 className={cx('mb-3 text-sm font-semibold', look.caps && 'uppercase tracking-wide')}>{t.contact}</h2>
              <ul className='space-y-2'>{contactLines.map((item, index) => line(item.icon, item.content, `c${index}`))}</ul>
            </div>
          ) : null}
          {site.pages.length > 1 ? (
            <div>
              <h2 className={cx('mb-3 text-sm font-semibold', look.caps && 'uppercase tracking-wide')}>{t.pages}</h2>
              <ul className='space-y-2'>
                {site.pages.map((page) => (
                  <li key={page.path}>
                    <Link to={page.path} className={cx('hover:underline', muted)}>
                      {page.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
        <p className={cx('mt-12 text-xs', muted)}>
          © {year} {site.name}
        </p>
      </div>
    </footer>
  )
}

export default Footer
