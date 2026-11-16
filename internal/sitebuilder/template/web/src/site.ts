// Written by the builder from the brief. Pages read the site's facts from here.
export type SitePage = { path: string; label: string }

export const site = {
  name: 'Website',
  tagline: '',
  language: 'en' as 'id' | 'en',
  pages: [{ path: '/', label: 'Home' }] as SitePage[],
  contact: { phone: '', whatsapp: '', email: '', address: '', hours: '' },
  social: {} as Record<string, string>,
  images: [] as { src: string; alt: string }[],
  logo: '',
}

/** A WhatsApp link with a prepared message, or '' when the site has no WhatsApp number. */
export function whatsappLink(message = ''): string {
  const number = site.contact.whatsapp.replace(/[^0-9]/g, '')
  if (!number) return ''
  return `https://wa.me/${number}${message ? `?text=${encodeURIComponent(message)}` : ''}`
}
