import { useEffect, useState } from 'react'
import { Menu, Phone, X } from 'lucide-react'
import { look } from '../look'
import { Link, usePath } from '../router'
import { site, whatsappLink } from '../site'
import { buttonClass, container, cx } from './ui'

type HeaderProps = {
  /** The words of the call button; left out to use the site's own words. */
  callLabel?: string
  /** A short line for the coloured bar above (looks with header "announce"), e.g. the opening hours or the delivery area. */
  note?: string
}

const words = {
  id: { call: 'Hubungi kami', menu: 'Buka menu', close: 'Tutup menu', nav: 'Menu utama' },
  en: { call: 'Contact us', menu: 'Open menu', close: 'Close menu', nav: 'Main menu' },
}

/** The bar at the top of every page: the name, the pages and one call button. On phones the pages fold into a menu button. */
export function Header({ callLabel, note }: HeaderProps) {
  const t = words[site.language] ?? words.id
  const path = usePath()
  const [open, setOpen] = useState(false)
  useEffect(() => setOpen(false), [path])

  const call = whatsappLink() || (site.contact.phone ? `tel:${site.contact.phone.replace(/[^0-9+]/g, '')}` : '')
  const callButton = call ? (
    <Link to={call} className={cx(buttonClass('primary'), 'min-h-10 px-5 py-2 text-sm')}>
      {callLabel ?? t.call}
    </Link>
  ) : null

  const name = (
    <Link to='/' className={cx('flex items-center gap-3 font-heading text-xl tracking-tight md:text-2xl', look.caps && 'font-black uppercase')}>
      {site.logo ? <img src={site.logo} alt='' className='h-10 w-auto' /> : null}
      <span>{site.name}</span>
    </Link>
  )

  const link = (page: { path: string; label: string }) => {
    const active = page.path === path
    const pill = look.button === 'pill' && look.frame === 'bold'
    return (
      <Link
        key={page.path}
        to={page.path}
        aria-current={active ? 'page' : undefined}
        className={cx(
          'transition-colors',
          pill
            ? cx('rounded-full border-2 border-border px-4 py-1.5 text-sm font-semibold', active ? 'bg-foreground text-background' : 'hover:bg-surface')
            : cx('py-1 text-sm', look.caps || look.header === 'centered' ? 'uppercase tracking-widest' : '', active ? 'text-foreground underline decoration-accent decoration-2 underline-offset-8' : 'text-muted hover:text-foreground'),
        )}
      >
        {page.label}
      </Link>
    )
  }

  const pages = site.pages.length > 1 ? site.pages : []
  const menuButton =
    pages.length > 0 ? (
      <button
        type='button'
        className='inline-flex size-11 items-center justify-center rounded-md border border-border md:hidden'
        aria-expanded={open}
        aria-controls='site-menu'
        aria-label={open ? t.close : t.menu}
        onClick={() => setOpen((value) => !value)}
      >
        {open ? <X className='size-5' /> : <Menu className='size-5' />}
      </button>
    ) : null
  const phoneMenu = open ? (
    <nav id='site-menu' aria-label={t.nav} className='border-t border-border md:hidden'>
      <div className={cx(container, 'flex flex-col gap-1 py-4')}>
        {pages.map((page) => (
          <Link key={page.path} to={page.path} aria-current={page.path === path ? 'page' : undefined} className='rounded-md px-2 py-3 text-lg hover:bg-surface'>
            {page.label}
          </Link>
        ))}
        {callButton ? <div className='pt-3'>{callButton}</div> : null}
      </div>
    </nav>
  ) : null

  const above =
    look.header === 'announce' && (note || site.contact.hours) ? (
      <p className='bg-accent px-5 py-2 text-center text-sm font-semibold text-on-accent'>{note || site.contact.hours}</p>
    ) : look.header === 'topbar' && (site.contact.phone || site.contact.email) ? (
      <div className='bg-primary text-primary-foreground'>
        <div className={cx(container, 'flex flex-wrap items-center justify-end gap-x-6 gap-y-1 py-2 text-sm')}>
          {site.contact.phone ? (
            <a href={`tel:${site.contact.phone.replace(/[^0-9+]/g, '')}`} className='inline-flex items-center gap-2 hover:underline'>
              <Phone className='size-4' aria-hidden='true' />
              {site.contact.phone}
            </a>
          ) : null}
          {site.contact.email ? (
            <a href={`mailto:${site.contact.email}`} className='hover:underline'>
              {site.contact.email}
            </a>
          ) : null}
        </div>
      </div>
    ) : null

  if (look.header === 'centered') {
    return (
      <header className='border-b border-border bg-background'>
        <div className={cx(container, 'flex items-center justify-between gap-4 py-6 md:flex-col md:justify-center md:gap-5')}>
          <div className='md:text-center'>{name}</div>
          <nav aria-label={t.nav} className='hidden flex-wrap justify-center gap-x-8 gap-y-2 md:flex'>
            {pages.map(link)}
          </nav>
          {menuButton}
        </div>
        {phoneMenu}
      </header>
    )
  }

  return (
    <header className={cx('bg-background', look.header === 'bar' ? 'border-b-2 border-border' : look.rule ? 'border-b-4 border-double border-accent' : 'border-b border-border')}>
      {above}
      <div className={cx(container, 'flex items-center justify-between gap-6 py-4 md:py-5')}>
        {name}
        <nav aria-label={t.nav} className='hidden flex-1 flex-wrap items-center justify-end gap-x-7 gap-y-2 md:flex'>
          {pages.map(link)}
        </nav>
        <div className='flex items-center gap-3'>
          <div className='hidden md:block'>{callButton}</div>
          {menuButton}
        </div>
      </div>
      {phoneMenu}
    </header>
  )
}

export default Header
