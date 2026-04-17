class Rinse < Formula
  desc "CLI automation tool for GitHub Copilot PR review workflows"
  homepage "https://github.com/orsharon7/rinse"
  url "https://github.com/orsharon7/rinse/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "0019dfc4b32d63c1392aa264aed2253c1e0c2fb09216f8e2cc269bbfb8bb49b5"
  license "MIT"

  depends_on "go" => :build

  def install
    # Build the TUI binary with version injection
    cd "tui" do
      system "go", "build",
             "-ldflags", "-X main.version=#{version}",
             "-o", libexec/"rinse-bin",
             "."
    end

    # Install pr-review helper scripts into libexec/pr-review subdirectory
    (libexec/"pr-review").install Dir["pr-review/*.sh"]
    (libexec/"pr-review").chmod_R 0755

    # Create a rinse wrapper that sets RINSE_SCRIPT_DIR so scripts are found
    # when users invoke rinse directly (including the interactive TUI).
    (bin/"rinse").write <<~EOS
      #!/usr/bin/env bash
      export RINSE_SCRIPT_DIR="#{libexec}/pr-review"
      exec "#{libexec}/rinse-bin" "$@"
    EOS
    chmod 0755, bin/"rinse"

    # Create a pr-review wrapper that also sets PR_REVIEW_SCRIPT_DIR
    (bin/"pr-review").write <<~EOS
      #!/usr/bin/env bash
      export RINSE_SCRIPT_DIR="#{libexec}/pr-review"
      export PR_REVIEW_SCRIPT_DIR="#{libexec}/pr-review"
      exec "#{libexec}/rinse-bin" "$@"
    EOS
    chmod 0755, bin/"pr-review"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/rinse --version 2>&1")
    assert_predicate bin/"pr-review", :executable?
  end
end
