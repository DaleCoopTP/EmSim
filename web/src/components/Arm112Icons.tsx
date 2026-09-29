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
// Main screen (list of incidents): toolbar tabs, header and grid row icons.
export const SearchIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M10.5 3a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15zM16 16l6 6" /></Svg>;
export const HeadsetIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2a9 9 0 0 0-9 9v7a3 3 0 0 0 3 3h2v-8H5v-2a7 7 0 0 1 14 0v2h-3v8h2a3 3 0 0 0 3-3v-7a9 9 0 0 0-9-9z" /></Svg>;
export const GearIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M19.4 13a7.5 7.5 0 0 0 0-2l2.1-1.6-2-3.5-2.5 1a7.4 7.4 0 0 0-1.7-1L15 3.3h-4l-.4 2.6a7.4 7.4 0 0 0-1.7 1l-2.5-1-2 3.5L6.6 11a7.5 7.5 0 0 0 0 2l-2.1 1.6 2 3.5 2.5-1a7.4 7.4 0 0 0 1.7 1l.4 2.6h4l.4-2.6a7.4 7.4 0 0 0 1.7-1l2.5 1 2-3.5zM13 15.5a3.5 3.5 0 1 1 0-7 3.5 3.5 0 0 1 0 7z" transform="translate(-1 0)" /></Svg>;
export const ExitIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M13.5 5.5a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM9.8 8.9 7 23h2.1l1.8-8 2.1 2v6h2v-7.5l-2.1-2 .6-3A7.3 7.3 0 0 0 19 13v-2a5 5 0 0 1-4.3-2.4l-1-1.6a2 2 0 0 0-1.7-1c-.3 0-.5.1-.8.1L6 8.3V13h2V9.6z" /></Svg>;
export const MenuIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2.4" d="M3 6h18M3 12h18M3 18h18" /></Svg>;
export const ScreenIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M14 3h7v7M21 3l-9 9M19 14v6H4V5h6" /></Svg>;
export const ChartIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2" d="M4 3v18h17M8 17v-5m4 5V8m4 9v-7" /></Svg>;
export const DocIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zm2 16H8v-2h8zm0-4H8v-2h8zm-3-5V3.5L18.5 9z" /></Svg>;
export const PeopleIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M16 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm-8 0a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm0 2c-2.3 0-7 1.2-7 3.5V19h14v-2.5C15 14.2 10.3 13 8 13zm8 0c-.3 0-.6 0-1 .1a4.2 4.2 0 0 1 2 3.4V19h6v-2.5c0-2.3-4.7-3.5-7-3.5z" /></Svg>;
export const HelicopterIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M2 4h14v1.6H9.8V8H13c3.9 0 7 2 7 4.5V15h-4.5l-2 2H18v1.6H7V17h4.3l-2-2H7.5L2 11.5V9.8L5 11V8h3.2V5.6H2z" /></Svg>;
export const GlassesIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="1.8" d="M6.5 11a4 4 0 1 0 0 8 4 4 0 0 0 0-8zm11 0a4 4 0 1 0 0 8 4 4 0 0 0 0-8zm-7 4.5a2 2 0 0 1 3 0M3 14l2-8h2m14 8-2-8h-2" /></Svg>;
export const EditIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="1.8" d="M4 4h10v2M4 4v16h16V10M9 15l1-4 8-8 3 3-8 8z" /></Svg>;
export const BadgeIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M20 5h-5V3h-6v2H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2zm-8 4a2.5 2.5 0 1 1 0 5 2.5 2.5 0 0 1 0-5zm5 9H7v-1c0-1.7 3.3-2.5 5-2.5s5 .8 5 2.5z" /></Svg>;
export const ChevronDownIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2.2" d="m6 9 6 6 6-6" /></Svg>;
export const ChevronUpIcon = (props: IconProps) => <Svg {...props}><path fill="none" stroke="currentColor" strokeWidth="2.2" d="m6 15 6-6 6 6" /></Svg>;
export const BookmarkIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M6 2h12v20l-6-4.5L6 22z" /></Svg>;
export const PushpinIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="m14.5 2 7.5 7.5-2 .5-3.5 3.5.5 4-2 2-4-4-5.5 5.5H4.5V20l5.5-5.5-4-4 2-2 4 .5L15.5 5.5z" /></Svg>;
export const ClipboardIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M19 4h-4.2a3 3 0 0 0-5.6 0H5v18h14zm-7-1a1 1 0 1 1 0 2 1 1 0 0 1 0-2zm4 15H8v-2h8zm0-4H8v-2h8zm0-4H8V8h8z" /></Svg>;
export const CheckCircleIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm-1.8 14.6L5.6 12l1.8-1.8 2.8 2.8 6.4-6.4L18.4 8.4z" /></Svg>;
export const InfoIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm1 15h-2v-6h2zm0-8h-2V7h2z" /></Svg>;
// DDS card flags (ЧС / ЧП) and the print button.
export const BoltIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M13 2 4 14h6l-1 8 9-12h-6z" /></Svg>;
export const WarningIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M12 2 1 21h22zm1 15h-2v-2h2zm0-4h-2V9h2z" /></Svg>;
export const PrintIcon = (props: IconProps) => <Svg {...props}><path fill="currentColor" d="M19 8H5a3 3 0 0 0-3 3v6h4v4h12v-4h4v-6a3 3 0 0 0-3-3zm-3 11H8v-5h8zm3-7a1 1 0 1 1 0-2 1 1 0 0 1 0 2zM18 3H6v4h12z" /></Svg>;
