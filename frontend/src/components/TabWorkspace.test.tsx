/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { TabPanel } from "./TabWorkspace";

// This project does not run vitest with `globals: true`, so RTL's automatic
// afterEach cleanup is never registered and renders would otherwise pile up
// across tests in the same file.
afterEach(cleanup);

/**
 * These pin the fix for the UI overhaul's headline problem.
 *
 * TabPanel used to render every tab and hide the inactive ones with a CSS
 * `hidden` class, so all 11 tabs' DOM was live at once — 44,773 elements and
 * 1,539 table rows measured in the running app, with Chrome needing over two
 * minutes to rasterize a single frame.
 *
 * "Hidden" is not "absent": the browser still builds, styles and lays out a
 * `display: none` subtree's elements. The distinction is the whole point, so
 * these assert on the DOM, not on visibility.
 */
describe("TabPanel", () => {
  it("renders an active tab's content", () => {
    render(<TabPanel active>hello</TabPanel>);
    expect(screen.getByText("hello")).toBeInTheDocument();
  });

  it("does not mount an inactive tab at all", () => {
    render(<TabPanel active={false}>hello</TabPanel>);
    // getByText would also pass for hidden content, so assert absence.
    expect(screen.queryByText("hello")).not.toBeInTheDocument();
  });

  it("never mounts a never-visited tab, even with keepAlive", () => {
    // Cold start: a keepAlive tab you have not opened yet must cost nothing.
    render(
      <TabPanel active={false} keepAlive>
        hello
      </TabPanel>,
    );
    expect(screen.queryByText("hello")).not.toBeInTheDocument();
  });

  it("keeps a visited keepAlive tab mounted after navigating away", () => {
    // This is the IndustryTab case: its plan builder holds unsaved drafts in
    // local state, so unmounting would silently discard an in-progress plan.
    const { rerender } = render(
      <TabPanel active keepAlive>
        hello
      </TabPanel>,
    );
    expect(screen.getByText("hello")).toBeInTheDocument();

    rerender(
      <TabPanel active={false} keepAlive>
        hello
      </TabPanel>,
    );
    expect(screen.queryByText("hello")).toBeInTheDocument();
  });

  it("unmounts a visited tab that did not opt into keepAlive", () => {
    const { rerender } = render(<TabPanel active>hello</TabPanel>);
    expect(screen.getByText("hello")).toBeInTheDocument();

    rerender(<TabPanel active={false}>hello</TabPanel>);
    expect(screen.queryByText("hello")).not.toBeInTheDocument();
  });
});
