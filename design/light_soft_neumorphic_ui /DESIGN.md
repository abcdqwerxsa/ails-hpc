---
name: Tactile Ethereal
colors:
  surface: '#fbf8ff'
  surface-dim: '#dbd9e3'
  surface-bright: '#fbf8ff'
  surface-container-lowest: '#ffffff'
  surface-container-low: '#f5f2fc'
  surface-container: '#efecf7'
  surface-container-high: '#e9e7f1'
  surface-container-highest: '#e3e1eb'
  on-surface: '#1b1b22'
  on-surface-variant: '#454653'
  inverse-surface: '#303037'
  inverse-on-surface: '#f2effa'
  outline: '#767684'
  outline-variant: '#c6c5d5'
  surface-tint: '#4552c3'
  primary: '#404dbe'
  on-primary: '#ffffff'
  primary-container: '#5a67d8'
  on-primary-container: '#faf7ff'
  inverse-primary: '#bdc2ff'
  secondary: '#526070'
  on-secondary: '#ffffff'
  secondary-container: '#d3e1f4'
  on-secondary-container: '#566474'
  tertiary: '#874a00'
  on-tertiary: '#ffffff'
  tertiary-container: '#aa5f00'
  on-tertiary-container: '#fff6f2'
  error: '#ba1a1a'
  on-error: '#ffffff'
  error-container: '#ffdad6'
  on-error-container: '#93000a'
  primary-fixed: '#dfe0ff'
  primary-fixed-dim: '#bdc2ff'
  on-primary-fixed: '#000965'
  on-primary-fixed-variant: '#2b38aa'
  secondary-fixed: '#d6e4f7'
  secondary-fixed-dim: '#bac8da'
  on-secondary-fixed: '#0f1d2a'
  on-secondary-fixed-variant: '#3b4857'
  tertiary-fixed: '#ffdcc1'
  tertiary-fixed-dim: '#ffb778'
  on-tertiary-fixed: '#2e1500'
  on-tertiary-fixed-variant: '#6c3a00'
  background: '#fbf8ff'
  on-background: '#1b1b22'
  surface-variant: '#e3e1eb'
typography:
  headline-lg:
    fontFamily: Montserrat
    fontSize: 32px
    fontWeight: '700'
    lineHeight: '1.2'
    letterSpacing: 0.02em
  headline-lg-mobile:
    fontFamily: Montserrat
    fontSize: 24px
    fontWeight: '700'
    lineHeight: '1.2'
    letterSpacing: 0.02em
  headline-md:
    fontFamily: Montserrat
    fontSize: 20px
    fontWeight: '600'
    lineHeight: '1.3'
    letterSpacing: 0.01em
  body-lg:
    fontFamily: Inter
    fontSize: 18px
    fontWeight: '400'
    lineHeight: '1.6'
    letterSpacing: 0.01em
  body-md:
    fontFamily: Inter
    fontSize: 16px
    fontWeight: '400'
    lineHeight: '1.5'
    letterSpacing: 0.01em
  label-caps:
    fontFamily: Inter
    fontSize: 12px
    fontWeight: '600'
    lineHeight: '1.2'
    letterSpacing: 0.08em
rounded:
  sm: 0.25rem
  DEFAULT: 0.5rem
  md: 0.75rem
  lg: 1rem
  xl: 1.5rem
  full: 9999px
spacing:
  unit: 8px
  container-padding: 24px
  gutter: 16px
  margin-mobile: 16px
  margin-desktop: 32px
---

## Brand & Style

The design system is centered on a **Neumorphic (Soft UI)** aesthetic, prioritizing a calm, professional, and modern emotional response. The interface should feel like a physical, molded surface where elements are extruded from or recessed into the background rather than floating on top of it.

The style relies on high-fidelity light play—using dual shadows (highlight and lowlight) to mimic real-world plastic or matte finishes. The overall mood is "Airy Minimalism," where whitespace is treated as a structural material, and the UI remains unobtrusive yet deeply tactile.

## Colors

The palette is monochromatic and low-contrast to facilitate the Neumorphic effect. The background color (`#F0F2F5`) is the "source" material for all UI elements. 

- **Primary**: Used sparingly for critical actions, status indicators, or active toggles. 
- **Neutral/Background**: The foundation for the entire UI.
- **Shadow Tokens**: These are functional colors. The light shadow (pure white) serves as the top-left "hit" of light, while the dark shadow (`#D1D9E6`) provides the bottom-right depth. 

All surfaces must match the background color hex exactly for the soft-molded effect to work.

## Typography

This design system utilizes a tiered typography scale to balance the "softness" of the UI with "sharp" information hierarchy. **Montserrat** is reserved for headlines to provide a confident, geometric anchor. **Inter** handles all functional and body text, ensuring high legibility against low-contrast backgrounds.

Generous letter spacing is applied to uppercase labels and body text to enhance the "airy" feel. Text color should remain slightly off-black (e.g., `#2D3748`) to avoid breaking the soft visual harmony.

## Layout & Spacing

The layout follows a **fluid grid system** with wide margins to emphasize the sense of space. Because Neumorphic elements require larger shadows to be effective, components must have significant "breathing room" (margins) between them to prevent shadow overlapping and visual clutter.

- **Desktop**: 12-column grid, 32px margins.
- **Mobile**: 4-column grid, 16px margins.
- **Rhythm**: All spacing (padding/margins) should be multiples of the 8px unit.

## Elevation & Depth

In this design system, elevation is not achieved via vertical displacement (Z-axis) but through "molding." 

1.  **Outset (Raised)**: Created using a `-5px -5px 10px` white shadow (top-left) and a `5px 5px 10px` `#D1D9E6` shadow (bottom-right). This is used for interactive buttons and primary cards.
2.  **Inset (Recessed)**: Created using an `inner-shadow` with the same logic. This is used for input fields, progress bar wells, and "pressed" button states.
3.  **Flat/Base**: The standard background level.

Avoid stacking more than two levels of depth (e.g., a raised button on a raised card). Most items should sit directly on the base background.

## Shapes

The shape language is consistently rounded to reinforce the "soft" and organic tactile feel. Sharp corners are strictly avoided as they break the illusion of a continuous, molded surface.

- **Standard Elements**: 16px (1rem) radius.
- **Large Cards/Containers**: 24px (1.5rem) radius.
- **Small Elements (Chips/Labels)**: 8px (0.5rem) radius.

## Components

### Buttons
Buttons use the **Outset** elevation style. On hover, the shadow blur increases slightly. On "active" or "pressed" states, the button transitions to **Inset**, creating a tactile "click" sensation.

### Cards
Cards are large containers with a 24px radius. They should appear slightly raised from the background. Avoid borders; use only the dual-shadow technique for definition.

### Input Fields
Inputs are always **Inset**. This signifies a "well" where data can be poured. The cursor and text should be clearly legible, and the inner shadow should be subtle enough not to clip the text.

### Chips & Toggles
Toggles use a small inset track with a raised (outset) circular thumb. Active states for chips can be indicated by a thin primary-colored outer glow or by switching from Outset to Inset.

### Progress Bars
The track is a long, thin Inset shape. The indicator is a flat or slightly raised bar in the primary color, sitting within the recessed track.