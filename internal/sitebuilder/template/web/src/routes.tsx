// Written by the builder from the brief: one route per page.
import type { ComponentType } from 'react'
import Home from './pages/Home'
import NotFound from './pages/NotFound'

export const routes: { path: string; title: string; component: ComponentType }[] = [
  { path: '/', title: 'Home', component: Home },
  { path: '*', title: 'Not found', component: NotFound },
]
