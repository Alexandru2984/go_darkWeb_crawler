import { describe, it, expect } from 'vitest'
import {
  CATEGORY_COLORS,
  CATEGORY_LABELS,
  allCategories,
  categoryColor,
  categoryLabel,
} from './categories.js'

describe('categories', () => {
  it('has a color and label for every category', () => {
    for (const cat of allCategories) {
      expect(CATEGORY_COLORS[cat], `color for ${cat}`).toMatch(/^#[0-9a-f]{6}$/i)
      expect(CATEGORY_LABELS[cat], `label for ${cat}`).toBeTruthy()
    }
  })

  it('includes the categories the backend can emit', () => {
    // Mirrors internal/crawler/categorizer.go.
    const backend = [
      'marketplace',
      'forum',
      'search-engine',
      'blog',
      'wiki',
      'directory',
      'news',
      'social',
      'hacking',
      'crypto-service',
      'hosting',
      'unknown',
    ]
    for (const cat of backend) {
      expect(allCategories, `missing ${cat}`).toContain(cat)
    }
  })

  it('categoryColor falls back to the unknown color', () => {
    expect(categoryColor('marketplace')).toBe(CATEGORY_COLORS.marketplace)
    expect(categoryColor('does-not-exist')).toBe(CATEGORY_COLORS.unknown)
  })

  it('categoryLabel falls back to the raw string', () => {
    expect(categoryLabel('wiki')).toBe(CATEGORY_LABELS.wiki)
    expect(categoryLabel('does-not-exist')).toBe('does-not-exist')
  })
})
