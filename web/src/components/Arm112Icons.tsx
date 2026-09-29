// Line icons for the ARM-112 card chrome. They only mirror the reference
// screen; every control that uses one also carries a text label for
// assistive technology.
import type { ReactNode } from "react";

type IconProps = { size?: number };

function Svg({ size = 20, children }: IconProps & { children: ReactNode }) {
  return <svg className="arm112-icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">{children}</svg>;
}

export const PhoneIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M6.6 10.8a15.1 15.1 0 0 0 6.6 6.6l2.2-2.2a1 1 0 0 1 1-.25 11.4 11.4 0 0 0 3.6.57 1 1 0 0 1 1 1V20a1 1 0 0 1-1 1A17 17 0 0 1 3 4a1 1 0 0 1 1-1h3.5a1 1 0 0 1 1 1c0 1.25.2 2.45.57 3.57a1 1 0 0 1-.25 1z" /></Svg>;
export const HangupIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 9c-1.6 0-3.15.25-4.6.72v3.1c0 .39-.23.74-.56.9-.98.49-1.87 1.12-2.66 1.85a.99.99 0 0 1-1.41-.02L.29 13.08a1 1 0 0 1 0-1.41C3.34 8.78 7.46 7 12 7s8.66 1.78 11.71 4.67a1 1 0 0 1 0 1.41l-2.48 2.47a.99.99 0 0 1-1.41.02 11.3 11.3 0 0 0-2.67-1.85 1 1 0 0 1-.56-.9v-3.1A15 15 0 0 0 12 9z" /></Svg>;
export const SmsIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M20 2H4a2 2 0 0 0-2 2v18l4-4h14a2 2 0 0 0 2-2V4a2 2 0 0 0-2-2zM8 11H6V9h2zm5 0h-2V9h2zm5 0h-2V9h2z" /></Svg>;
export const GlobeIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm-1 17.93A8 8 0 0 1 4.07 11H7v1a2 2 0 0 0 2 2v1a2 2 0 0 0 2 2zm6.9-2.54A2 2 0 0 0 16 16h-1v-3a1 1 0 0 0-1-1H8v-2h2a1 1 0 0 0 1-1V7h2a2 2 0 0 0 2-2v-.41a8 8 0 0 1 2.9 12.8z" /></Svg>;
export const PinIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2a7 7 0 0 0-7 7c0 5.25 7 13 7 13s7-7.75 7-13a7 7 0 0 0-7-7zm0 9.5A2.5 2.5 0 1 1 12 6.5a2.5 2.5 0 0 1 0 5z" /></Svg>;
export const HelpIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm1 17h-2v-2h2zm2.07-7.75-.9.92A3.4 3.4 0 0 0 13 15h-2v-.5a4 4 0 0 1 1.17-2.83l1.24-1.26A2 2 0 1 0 10 9H8a4 4 0 1 1 7.07 2.25z" /></Svg>;
export const MapIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M9 4 3 6v14l6-2 6 2 6-2V4l-6 2-6-2zm0 0v14m6-12v14" /></Svg>;
export const TranslateIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="m12.87 15.07-2.54-2.51.03-.03A17.5 17.5 0 0 0 14.07 6H17V4h-7V2H8v2H1v2h11.17A15.7 15.7 0 0 1 9 11.35 15.6 15.6 0 0 1 6.69 8h-2a17.6 17.6 0 0 0 2.98 4.56l-5.09 5.02L4 19l5-5 3.11 3.11zM18.5 10h-2L12 22h2l1.12-3h4.75L21 22h2zm-2.62 7 1.62-4.33L19.12 17z" /></Svg>;
export const LinkIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1M17 16v6m-3-3h6" /></Svg>;
export const StopwatchIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M9 2h6m-3 5v6l3 2m-3 7a8 8 0 1 0 0-16 8 8 0 0 0 0 16z" /></Svg>;
export const BellIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 22a2 2 0 0 0 2-2h-4a2 2 0 0 0 2 2zm6-6V11a6 6 0 0 0-5-5.9V4a1 1 0 0 0-2 0v1.1A6 6 0 0 0 6 11v5l-2 2v1h16v-1zM3.5 4.2 2.1 2.8A11 11 0 0 0 0 9h2a9 9 0 0 1 1.5-4.8zM22 9h2a11 11 0 0 0-2.1-6.2l-1.4 1.4A9 9 0 0 1 22 9z" /></Svg>;
export const MessageIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M20 2H4a2 2 0 0 0-2 2v18l4-4h14a2 2 0 0 0 2-2V4a2 2 0 0 0-2-2zm-7 12h-2v-2h2zm0-4h-2V6h2z" /></Svg>;
export const CloseIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2.4" d="m5 5 14 14M19 5 5 19" /></Svg>;
export const PlusIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M12 3v18M3 12h18" /></Svg>;
export const MicIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11z" /></Svg>;
