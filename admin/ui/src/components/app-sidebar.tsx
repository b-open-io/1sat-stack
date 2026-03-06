import { Globe, Coins, Settings, Users, ChevronDown } from "lucide-react";
import { useLocation, Link } from "react-router-dom";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarFooter,
} from "@/components/ui/sidebar";

const navItems = [
  { title: "OpNS", url: "/", icon: Globe },
  { title: "BSV21", url: "/bsv21", icon: Coins },
  { title: "Users", url: "/users", icon: Users },
  { title: "System", url: "/system", icon: Settings },
];

interface AppSidebarProps {
  connected: boolean;
  connecting: boolean;
  identityKey: string | null;
  onConnect: () => void;
  onConnectOneSat: () => void;
  onCopyIdentity: () => void;
}

export function AppSidebar({
  connected,
  connecting,
  identityKey,
  onConnect,
  onConnectOneSat,
  onCopyIdentity,
}: AppSidebarProps) {
  const location = useLocation();

  return (
    <Sidebar>
      <SidebarHeader className="p-4 border-b border-sidebar-border">
        <h1 className="text-lg font-semibold text-sidebar-foreground">
          <span className="text-primary">1Sat</span> Stack
        </h1>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {navItems.map((item) => {
                const isActive =
                  item.url === "/"
                    ? location.pathname === "/"
                    : location.pathname.startsWith(item.url);
                return (
                  <SidebarMenuItem key={item.title}>
                    <SidebarMenuButton asChild isActive={isActive}>
                      <Link to={item.url}>
                        <item.icon />
                        <span>{item.title}</span>
                      </Link>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                );
              })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter className="p-4 border-t border-sidebar-border">
        {connected ? (
          <div className="flex items-center gap-2">
            <span className="inline-block w-2 h-2 rounded-full bg-success" />
            <span className="text-xs text-sidebar-foreground">Connected</span>
            {identityKey && (
              <span
                className="text-xs font-mono bg-secondary rounded px-1.5 py-0.5 cursor-pointer truncate max-w-[140px] text-sidebar-muted-foreground hover:text-sidebar-foreground"
                title={`Click to copy: ${identityKey}`}
                onClick={onCopyIdentity}
              >
                {identityKey.slice(0, 8)}...{identityKey.slice(-8)}
              </span>
            )}
          </div>
        ) : (
          <div className="flex w-full">
            <button
              onClick={onConnect}
              disabled={connecting}
              className="flex-1 px-3 py-2 text-sm font-medium rounded-l-md bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {connecting ? "Connecting..." : "Connect Wallet"}
            </button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  disabled={connecting}
                  className="px-1.5 py-2 rounded-r-md border-l border-primary-foreground/20 bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  <ChevronDown className="size-4" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={onConnect}>
                  Auto-detect
                </DropdownMenuItem>
                <DropdownMenuItem onClick={onConnectOneSat}>
                  1Sat Wallet
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        )}
      </SidebarFooter>
    </Sidebar>
  );
}
