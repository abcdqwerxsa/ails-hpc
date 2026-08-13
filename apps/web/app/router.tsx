import { createRouter as createTanStackRouter, createHashHistory } from '@tanstack/react-router';
import { routeTree } from './routeTree.gen';

export function createRouter() {
  const router = createTanStackRouter({
    routeTree,
    defaultPreload: 'intent',
    // hash 路由：gin.Static 服务 SPA 时无 fallback，深链刷新会 404；hash 路由规避此问题
    history: createHashHistory(),
  });

  return router;
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createRouter>;
  }
}
